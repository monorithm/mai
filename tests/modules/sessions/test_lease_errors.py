"""Each failure of a lease request, a heartbeat, or a bind has an error code."""

import anyio
import pytest

from mai.modules.credentials import AccountStateError, UnknownAccountError
from mai.modules.sessions.application.lease_service import LeaseError
from mai.platform.contract import ToolError
from tests.modules.sessions.test_lease_service import make_parts


def lease_error(fn, *args) -> LeaseError:
    with pytest.raises(LeaseError) as info:
        anyio.run(fn, *args)
    return info.value


def failing(code):
    async def log_in(command):
        raise ToolError(code)

    return log_in


@pytest.mark.parametrize(
    ("code", "status", "retry_safe"),
    [
        ("MAIL_SERVER_DOWN", 503, True),
        ("STORE_UNAVAILABLE", 503, True),
        ("LOGIN_FAILED", 502, False),
        ("CREDENTIAL_REVOKED", 502, False),
    ],
)
def test_a_mail_login_failure_of_a_lease_request_gives_its_code(
    tmp_path, monkeypatch, code, status, retry_safe
):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    monkeypatch.setattr(parts.log_in, "execute", failing(code))
    for name in ("lane-a", None):
        error = lease_error(parts.leases.acquire, parts.key, "cursor", "/w", 1, name)
        assert (error.code, error.status, error.retry_safe) == (
            code,
            status,
            retry_safe,
        )
        assert str(error) == ToolError(code).message
    # A failed lease request keeps no lease.
    assert anyio.run(parts.leases.leases) == []
    monkeypatch.undo()
    named = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 2, "lane-a")
    pool = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 3)
    assert (named["session"], pool["session"]) == ("cursor.lane-a", "cursor.s1")


def test_a_revoke_before_the_rotate_gives_session_revoked(tmp_path, monkeypatch):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    rotate = parts.credentials.session_rotate

    async def revoke_then_rotate(login, file=None):
        # The owner revokes the session after the status check of the lease request.
        await parts.credentials.session_revoke(login)
        return await rotate(login, file)

    monkeypatch.setattr(parts.credentials, "session_rotate", revoke_then_rotate)
    error = lease_error(parts.leases.acquire, parts.key, "cursor", "/w", 1, "lane-a")
    assert (error.code, error.status) == ("SESSION_REVOKED", None)
    assert anyio.run(parts.leases.leases) == []


@pytest.mark.parametrize(
    ("exc", "code"),
    [
        (AccountStateError("session is revoked"), "SESSION_REVOKED"),
        (UnknownAccountError("unknown session login"), "SESSION_UNKNOWN"),
        (RuntimeError("the database does not reply"), "STORE_UNAVAILABLE"),
    ],
)
def test_a_rotate_failure_of_a_lease_request_gives_a_code(
    tmp_path, monkeypatch, exc, code
):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")

    async def rotate(login, file=None):
        raise exc

    monkeypatch.setattr(parts.credentials, "session_rotate", rotate)
    for name in ("lane-a", None):
        error = lease_error(parts.leases.acquire, parts.key, "cursor", "/w", 1, name)
        assert error.code == code


def test_a_store_failure_of_the_key_check_is_not_bad_key(tmp_path, monkeypatch):
    parts = make_parts(tmp_path)

    async def check(key):
        raise RuntimeError("the database does not reply")

    monkeypatch.setattr(parts.keys, "check", check)
    error = lease_error(parts.leases.acquire, parts.key, "cursor", "/w", 1)
    assert (error.code, error.status, error.retry_safe) == (
        "STORE_UNAVAILABLE",
        503,
        True,
    )


def test_a_store_failure_of_the_session_list_gives_store_unavailable(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)

    async def session_list(agent=None):
        raise RuntimeError("the database does not reply")

    monkeypatch.setattr(parts.credentials, "session_list", session_list)
    for name in ("lane-a", None):
        error = lease_error(parts.leases.acquire, parts.key, "cursor", "/w", 1, name)
        assert (error.code, error.status) == ("STORE_UNAVAILABLE", 503)


def test_a_failed_count_of_new_mail_gives_null_and_keeps_the_lease(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    lease = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1)["lease"]

    async def count(state):
        raise OSError("the mail server does not reply")

    monkeypatch.setattr(parts.counter, "count", count)
    beat = anyio.run(parts.leases.heartbeat, lease)
    assert beat == {"session": "cursor.s1", "new_mail": None}
    parts.counter.value = None
    monkeypatch.undo()
    beat = anyio.run(parts.leases.heartbeat, lease)
    assert beat == {"session": "cursor.s1", "new_mail": None}


def test_a_store_failure_of_a_heartbeat_gives_503_and_keeps_the_lease(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    lease = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1)["lease"]

    async def execute(query):
        raise ToolError("STORE_UNAVAILABLE")

    monkeypatch.setattr(parts.resolve, "execute", execute)
    error = lease_error(parts.leases.heartbeat, lease)
    assert (error.code, error.status, error.retry_safe) == (
        "STORE_UNAVAILABLE",
        503,
        True,
    )
    with pytest.raises(ToolError) as info:
        anyio.run(parts.leases.state, lease)
    assert info.value.code == "STORE_UNAVAILABLE"
    monkeypatch.undo()
    beat = anyio.run(parts.leases.heartbeat, lease)
    assert beat == {"session": "cursor.s1", "new_mail": 3}


def test_a_mail_login_failure_of_a_bind_gives_its_tool_error(tmp_path, monkeypatch):
    parts = make_parts(tmp_path)
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(8)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    anyio.run(parts.leases.end, pool[0]["lease"])
    monkeypatch.setattr(parts.log_in, "execute", failing("MAIL_SERVER_DOWN"))
    with pytest.raises(ToolError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "MAIL_SERVER_DOWN"
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": None, "new_mail": 0}
    monkeypatch.undo()
    assert anyio.run(parts.leases.state, extra["lease"]).login == "cursor.s1"
