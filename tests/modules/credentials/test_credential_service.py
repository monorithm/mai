"""The commands of the credentials module on the memory store."""

import os
import stat

import anyio
import pytest

from mai.modules.credentials.application.add_agent_command import AddAgentHandler
from mai.modules.credentials.application.authenticate_command import (
    AuthenticateHandler,
)
from mai.modules.credentials.application.check_login_query import CheckLoginHandler
from mai.modules.credentials.application.credential_service import (
    CredentialService,
)
from mai.modules.credentials.application.get_account_kind_query import (
    GetAccountKindHandler,
)
from mai.modules.credentials.application.get_roles_query import GetRolesHandler
from mai.modules.credentials.application.grant_mailbox_access_command import (
    GrantMailboxAccessHandler,
)
from mai.modules.credentials.application.init_accounts_command import (
    InitAccountsHandler,
)
from mai.modules.credentials.application.list_agents_query import ListAgentsHandler
from mai.modules.credentials.application.list_sessions_query import (
    ListSessionsHandler,
)
from mai.modules.credentials.application.new_session_command import (
    NewSessionHandler,
)
from mai.modules.credentials.application.revoke_session_command import (
    RevokeSessionHandler,
)
from mai.modules.credentials.application.rotate_session_command import (
    RotateSessionHandler,
)
from mai.modules.credentials.domain.credential import (
    ACL_RIGHTS,
    parse_credential,
)
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
from mai.modules.credentials.infra.bcrypt_secret_hasher import BcryptSecretHasher
from mai.modules.credentials.infra.in_memory_credential_store import (
    InMemoryCredentialStore,
)
from mai.modules.credentials.infra.local_credential_files import (
    LocalCredentialFiles,
)
from mai.platform.contract import ToolError


class RecordingAcl(MailboxAcl):
    def __init__(self) -> None:
        self.calls: list[tuple] = []
        self.down = False

    async def grant(self, agent, login, rights):
        self.calls.append(("grant", agent, login, tuple(rights)))
        return not self.down

    async def remove(self, agent, login):
        self.calls.append(("remove", agent, login))
        return True

    async def return_unread(self, agent, login):
        self.calls.append(("return", agent, login))
        return True


def make_service(tmp_path):
    store = InMemoryCredentialStore()
    hasher = BcryptSecretHasher()
    files = LocalCredentialFiles(str(tmp_path / "secrets"))
    acl = RecordingAcl()
    service = CredentialService(
        InitAccountsHandler(store, hasher, files, acl),
        NewSessionHandler(store, hasher, files, acl),
        ListSessionsHandler(store),
        RotateSessionHandler(store, hasher, files),
        RevokeSessionHandler(store, acl),
        AuthenticateHandler(store, hasher),
        CheckLoginHandler(store),
        GetRolesHandler(store),
        files,
        GrantMailboxAccessHandler(store, acl),
        AddAgentHandler(store, acl),
        ListAgentsHandler(store),
        GetAccountKindHandler(store),
    )
    return service, store, acl, files


# No agent exists at the start: each test adds the agents that it uses.
TEST_AGENTS = ("cursor", "codex")


async def prepare(service, write_secrets=False):
    """Make the system accounts and the agents of the tests."""
    await service.init_accounts(write_secrets=write_secrets)
    for name in TEST_AGENTS:
        await service.agent_add(name)


def run(fn):
    return anyio.run(fn)


def test_init_accounts_is_idempotent_and_grants_board_rights(tmp_path):
    service, _store, acl, files = make_service(tmp_path)

    async def go():
        first = await service.init_accounts()
        await service.agent_add("cursor")
        second = await service.init_accounts()
        return first, second

    first, second = run(go)
    assert sorted(first["created"]) == ["board", "copies", "owner"]
    assert first["agents"] == []
    assert second["created"] == [] and second["agents"] == ["cursor"]
    assert ("grant", "cursor", "board", ("lookup", "read")) in acl.calls
    path = files.system_path("owner")
    assert stat.S_IMODE(os.stat(path).st_mode) == 0o600
    owner = files.read(path)

    async def owner_login():
        return await service.authenticate_owner(owner)

    assert run(owner_login)["kind"] == "owner"


def test_session_lifecycle(tmp_path):
    service, _store, acl, _files = make_service(tmp_path)

    async def go():
        await prepare(service)
        issued = await service.session_new("cursor", "lane-a", expires_days="7d")
        login, secret = parse_credential(issued["credential"])
        principal = await service.authenticate(login, secret)
        listed = await service.session_list("cursor")
        fp1 = await service.login_fingerprint(login)
        rotated = await service.session_rotate(login)
        fp2 = await service.login_fingerprint(login)
        with pytest.raises(ToolError) as old:
            await service.authenticate(login, secret)
        _l, new_secret = parse_credential(rotated["credential"])
        await service.authenticate(login, new_secret)
        await service.session_revoke(login)
        with pytest.raises(ToolError) as revoked:
            await service.authenticate(login, new_secret)
        return (
            issued,
            principal,
            listed,
            fp1,
            fp2,
            old.value,
            revoked.value,
            await service.login_fingerprint(login),
        )

    issued, principal, listed, fp1, fp2, old, revoked, fp3 = run(go)
    assert issued["login"] == "cursor.lane-a"
    assert issued["address"] == "cursor.lane-a@mai.internal"
    assert principal["status"] == "active" and principal["agent"] == "cursor"
    assert "password_hash" not in listed[0]
    assert fp1 and fp2 and fp1 != fp2
    assert old.code == "LOGIN_FAILED"
    assert revoked.code == "CREDENTIAL_REVOKED"
    assert fp3 is None
    assert ("grant", "cursor", "cursor.lane-a", ACL_RIGHTS) in acl.calls
    assert ("remove", "cursor", "cursor.lane-a") in acl.calls


def test_bad_inputs(tmp_path):
    service, _store, _acl, _files = make_service(tmp_path)

    async def go():
        await prepare(service)
        with pytest.raises(ValueError):
            await service.session_new("nobody", "x")
        with pytest.raises(ValueError):
            await service.session_new("cursor", "Bad Label")
        await service.session_new("cursor", "a")
        with pytest.raises(ValueError):
            await service.session_new("cursor", "a")
        with pytest.raises(ToolError) as agent_login:
            await service.authenticate("cursor", "a" * 52)
        return agent_login.value

    assert run(go).code == "LOGIN_FAILED"
    with pytest.raises(ToolError):
        service.parse_credential("mai1:cursor")
    with pytest.raises(ToolError):
        service.read_credential_file(str(tmp_path / "missing.cred"))


def test_expired_session(tmp_path):
    service, _store, _acl, _files = make_service(tmp_path)

    async def go():
        await prepare(service)
        issued = await service.session_new("codex", "old", expires_days=-1)
        login, secret = parse_credential(issued["credential"])
        with pytest.raises(ToolError) as exc:
            await service.authenticate(login, secret)
        listed = await service.session_list()
        return exc.value, listed

    exc, listed = run(go)
    assert exc.code == "CREDENTIAL_EXPIRED"
    assert listed[0]["status"] == "expired"


def test_hash_has_dovecot_form():
    hasher = BcryptSecretHasher()
    stored = hasher.hash("secret-value")
    assert stored.startswith("{BLF-CRYPT}$2y$")
    assert hasher.verify("secret-value", stored)
    assert not hasher.verify("other", stored)


def test_mailbox_grants_give_the_board_and_each_live_session_its_rights(tmp_path):
    service, _store, acl, _files = make_service(tmp_path)

    async def go():
        # Before Dovecot runs, each grant fails, as at the first start of the container.
        acl.down = True
        await prepare(service)
        await service.session_new("cursor", "lane-a")
        await service.session_new("cursor", "lane-b")
        await service.session_revoke("cursor.lane-b")
        acl.down = False
        acl.calls.clear()
        return await service.grant_mailbox_access()

    result = run(go)
    assert result["failed"] == []
    for agent in TEST_AGENTS:
        assert ("grant", agent, "board", ("lookup", "read")) in acl.calls
    assert ("grant", "cursor", "cursor.lane-a", ACL_RIGHTS) in acl.calls
    assert not [c for c in acl.calls if c[2] == "cursor.lane-b"]


def test_mailbox_grants_report_each_grant_that_fails(tmp_path, caplog):
    service, _store, acl, _files = make_service(tmp_path)

    async def go():
        await prepare(service)
        acl.down = True
        return await service.grant_mailbox_access()

    with caplog.at_level("WARNING", logger="mai.credentials"):
        result = run(go)
    assert ("cursor", "board") in result["failed"]
    assert any("board" in r.getMessage() for r in caplog.records)


def test_revoke_returns_the_unread_mail_before_it_removes_the_rights(tmp_path):
    service, _store, acl, _files = make_service(tmp_path)

    async def go():
        await prepare(service)
        await service.session_new("cursor", "lane-a")
        acl.calls.clear()
        await service.session_revoke("cursor.lane-a")

    run(go)
    assert acl.calls == [
        ("return", "cursor", "cursor.lane-a"),
        ("remove", "cursor", "cursor.lane-a"),
    ]


def test_revoke_keeps_the_queue_rights_when_the_return_fails(tmp_path):
    service, _store, acl, _files = make_service(tmp_path)

    async def failing_return(agent, login):
        acl.calls.append(("return", agent, login))
        return False

    async def go():
        await prepare(service)
        await service.session_new("cursor", "lane-a")
        acl.calls.clear()
        acl.return_unread = failing_return
        await service.session_revoke("cursor.lane-a")

    run(go)
    assert acl.calls == [("return", "cursor", "cursor.lane-a")]


def test_agent_add_makes_an_active_agent_and_grants_the_board_read_on_its_queue(
    tmp_path,
):
    service, store, acl, _files = make_service(tmp_path)

    async def go():
        await service.init_accounts(write_secrets=False)
        first = await service.agent_add("reviewer")
        again = await service.agent_add("reviewer")
        return first, again, await store.get("reviewer")

    first, again, principal = run(go)
    assert first == {
        "agent": "reviewer",
        "address": "reviewer@mai.internal",
        "created": True,
        "acl_set": True,
    }
    assert again["created"] is False
    assert principal is not None
    assert (principal.kind, principal.status) == ("agent", "active")
    assert ("grant", "reviewer", "board", ("lookup", "read")) in acl.calls


@pytest.mark.parametrize(
    "name", ["", "Bad", "a.b", "-a", "owner", "board", "a" * 49, "x y"]
)
def test_agent_add_refuses_a_name_that_is_not_a_valid_agent_name(tmp_path, name):
    service, _store, _acl, _files = make_service(tmp_path)

    async def go():
        await service.init_accounts(write_secrets=False)
        await service.agent_add(name)

    with pytest.raises(ValueError):
        run(go)


def test_agent_names_give_only_the_active_agents_in_order(tmp_path):
    service, store, _acl, _files = make_service(tmp_path)

    async def go():
        await service.init_accounts(write_secrets=False)
        for name in ("writer", "architect", "old"):
            await service.agent_add(name)
        await store.set_status("old", "revoked", None)
        await service.session_new("writer", "a")
        return await service.agent_names(), await service.agent_list()

    names, rows = run(go)
    assert names == ["architect", "writer"]
    assert [(r["agent"], r["status"], r["sessions"]) for r in rows] == [
        ("architect", "active", 0),
        ("old", "revoked", 0),
        ("writer", "active", 1),
    ]


def test_a_session_of_an_agent_that_does_not_exist_names_the_agent_add_command(
    tmp_path,
):
    service, _store, _acl, _files = make_service(tmp_path)

    async def go():
        await service.init_accounts(write_secrets=False)
        await service.session_new("ghost", "a")

    with pytest.raises(ValueError, match="mai agent add ghost"):
        run(go)


def test_account_kind_tells_the_kind_or_none(tmp_path):
    service, _store, _acl, _files = make_service(tmp_path)

    async def go():
        await prepare(service)
        return [
            await service.account_kind(n) for n in ("owner", "board", "cursor", "x")
        ]

    assert run(go) == ["owner", "system", "agent", None]


COPIES_RIGHTS = (
    "lookup",
    "read",
    "write",
    "write-seen",
    "write-deleted",
    "expunge",
)


def test_init_accounts_makes_copies_with_no_secret_and_an_owner_grant(tmp_path):
    service, store, acl, files = make_service(tmp_path)

    async def go():
        await service.init_accounts()
        return await store.get("copies")

    copies = run(go)
    assert copies is not None
    assert copies.kind == "system"
    assert copies.password_hash is None
    assert files.read(files.system_path("copies")) is None
    assert ("grant", "copies", "owner", COPIES_RIGHTS) in acl.calls
    assert not [c for c in acl.calls if c[1] == "copies" and c[2] != "owner"]


def test_grant_mailbox_access_gives_the_owner_the_copies(tmp_path):
    service, _store, acl, _files = make_service(tmp_path)

    async def go():
        await service.init_accounts(write_secrets=False)
        acl.calls.clear()
        return await service.grant_mailbox_access()

    result = run(go)
    assert ("copies", "owner") in result["granted"]
    assert acl.calls[0] == ("grant", "copies", "owner", COPIES_RIGHTS)


def test_copies_is_not_an_agent_name(tmp_path):
    service, _store, _acl, _files = make_service(tmp_path)

    async def go():
        await service.agent_add("copies")

    with pytest.raises(ValueError, match="system account"):
        run(go)
