"""The tools login, logout, and whoami on the memory stores with a fake mail server."""

from typing import cast

import anyio
import pytest

from mai.modules.credentials.domain.credential import parse_credential
from mai.modules.sessions.api.session_tools import SessionTools
from mai.modules.sessions.application.log_in_command import LogInHandler
from mai.modules.sessions.application.log_out_command import LogOutHandler
from mai.modules.sessions.application.resolve_session_query import (
    ResolveSessionHandler,
)
from mai.modules.sessions.application.session_cache import SessionCache
from mai.modules.sessions.application.session_service import SessionService
from mai.modules.sessions.infra.in_memory_handle_store import InMemoryHandleStore
from mai.platform.contract import Ctx, ToolError
from mai.platform.mailserver.mail_client_factory import MailClientFactory
from tests.modules.credentials.test_credential_service import make_service, prepare


class FakeMail:
    def __init__(self, login):
        self.login = login
        self.closed = False

    def connect(self):
        return self

    def close(self):
        self.closed = True


class FakeFactory:
    def __init__(self):
        self.opened = []

    def open(self, login, secret):
        mail = FakeMail(login)
        self.opened.append(mail)
        return mail


def make_tools(tmp_path):
    credentials, store, _acl, _files = make_service(tmp_path)
    handles = InMemoryHandleStore()
    cache = SessionCache()
    factory = FakeFactory()
    resolve = ResolveSessionHandler(credentials, handles, cache)
    service = SessionService(
        LogInHandler(
            credentials,
            handles,
            # The fake gives fake sessions, not MailSession objects.
            cast(MailClientFactory, factory),
            cache,
        ),
        resolve,
        LogOutHandler(resolve, handles),
    )
    return SessionTools(service), credentials, store, factory, tmp_path


def test_stdio_login_whoami_logout(tmp_path):
    tools, credentials, store, factory, _ = make_tools(tmp_path)
    store.set_role("cursor", "api", "lane")

    async def go():
        await prepare(credentials)
        issued = await credentials.session_new("cursor", "a")
        ctx = Ctx(transport="stdio")
        before = await tools.whoami(ctx, {})
        result = await tools.login(
            ctx, {"credential": issued["credential"], "client": "test"}
        )
        me = await tools.whoami(ctx, {})
        ended = await tools.logout(ctx, {})
        after = await tools.whoami(ctx, {})
        with pytest.raises(ToolError) as exc:
            await tools.whoami(Ctx(transport="http"), {"session": result["session"]})
        return before, result, me, ended, after, exc.value

    before, result, me, ended, after, exc = anyio.run(go)
    assert before["logged_in"] is False
    assert result["session"].startswith("ses-") and len(result["session"]) == 30
    assert result["roles"] == {"api": "lane"} and result["projects"] == ["api"]
    assert me["logged_in"] and me["login"] == "cursor.a" and me["agent"] == "cursor"
    assert ended == {"ended": True}
    assert after["logged_in"] is False
    assert exc.code == "SESSION_UNKNOWN"
    assert factory.opened[0].closed


def test_rotate_and_revoke_end_handles(tmp_path):
    tools, credentials, _store, _factory, _ = make_tools(tmp_path)

    async def go():
        await prepare(credentials)
        issued = await credentials.session_new("codex", "a")
        http = Ctx(transport="http")
        first = await tools.login(http, {"credential": issued["credential"]})
        rotated = await credentials.session_rotate("codex.a")
        with pytest.raises(ToolError) as after_rotate:
            await tools.whoami(http, {"session": first["session"]})
        second = await tools.login(http, {"credential": rotated["credential"]})
        await tools.whoami(http, {"session": second["session"]})
        await credentials.session_revoke("codex.a")
        with pytest.raises(ToolError) as after_revoke:
            await tools.whoami(http, {"session": second["session"]})
        with pytest.raises(ToolError) as again:
            await tools.login(http, {"credential": rotated["credential"]})
        return after_rotate.value, after_revoke.value, again.value

    after_rotate, after_revoke, again = anyio.run(go)
    assert after_rotate.code == "SESSION_UNKNOWN"
    assert after_revoke.code == "SESSION_UNKNOWN"
    assert again.code == "CREDENTIAL_REVOKED"


def test_login_arguments_and_file(tmp_path):
    tools, credentials, _store, _factory, _ = make_tools(tmp_path)

    async def go():
        await prepare(credentials)
        path = str(tmp_path / "cursor.cred")
        await credentials.session_new("cursor", "f", file=path)
        http = Ctx(transport="http")
        ok = await tools.login(http, {"credential_file": path})
        errors = []
        for args in (
            {},
            {"credential": "x", "credential_file": path},
            {"credential_file": str(tmp_path / "no")},
            {"credential": "mai1:cursor.f:" + "a" * 52},
            {"credential": "c", "client": 3},
        ):
            with pytest.raises(ToolError) as exc:
                await tools.login(http, args)
            errors.append(exc.value.code)
        with pytest.raises(ToolError) as stdio:
            await tools.logout(Ctx(transport="stdio"), {})
        return ok, errors, stdio.value.code

    ok, errors, stdio = anyio.run(go)
    assert ok["login"] == "cursor.f"
    assert errors == [
        "INVALID_ARGUMENT",
        "INVALID_ARGUMENT",
        "LOGIN_FAILED",
        "LOGIN_FAILED",
        "INVALID_ARGUMENT",
    ]
    assert stdio == "NOT_LOGGED_IN"
    assert parse_credential("mai1:cursor.f@mai.internal:" + "a" * 52)[0] == "cursor.f"
