"""The leases of the sessions on the memory stores with a fake mail server."""

import datetime as dt
from dataclasses import dataclass
from typing import cast

import anyio
import pytest

from mai.modules.credentials.application.add_agent_command import AddAgentHandler
from mai.modules.credentials.application.agent_keys import AgentKeys
from mai.modules.credentials.application.credential_service import (
    CredentialService,
)
from mai.modules.credentials.infra.bcrypt_secret_hasher import BcryptSecretHasher
from mai.modules.credentials.infra.in_memory_agent_key_store import (
    InMemoryAgentKeyStore,
)
from mai.modules.sessions.application.lease_service import LeaseError, LeaseService
from mai.modules.sessions.application.log_in_command import LogInHandler
from mai.modules.sessions.application.log_out_command import LogOutHandler
from mai.modules.sessions.application.resolve_session_query import (
    ResolveSessionHandler,
)
from mai.modules.sessions.application.session_cache import SessionCache
from mai.modules.sessions.application.session_service import SessionService
from mai.modules.sessions.domain.lease import is_token
from mai.modules.sessions.domain.new_mail_counter import NewMailCounter
from mai.modules.sessions.infra.in_memory_handle_store import InMemoryHandleStore
from mai.modules.sessions.infra.in_memory_lease_store import InMemoryLeaseStore
from mai.platform.mailserver.mail_client_factory import MailClientFactory
from tests.modules.credentials.test_credential_service import make_service
from tests.modules.sessions.test_session_tools import FakeFactory


class FixedCounter(NewMailCounter):
    def __init__(self) -> None:
        self.value: int | None = 3

    async def count(self, state) -> int | None:
        return self.value


@dataclass
class Parts:
    leases: LeaseService
    credentials: CredentialService
    keys: AgentKeys
    lease_store: InMemoryLeaseStore
    key: str
    # The session service on the same handles as the lease service, for the MCP tools.
    sessions: SessionService
    factory: FakeFactory
    counter: FixedCounter
    log_in: LogInHandler
    resolve: ResolveSessionHandler


def make_parts(tmp_path) -> Parts:
    """A lease service on memory stores, with the agent cursor and its key."""
    credentials, store, acl, _files = make_service(tmp_path)
    keys = AgentKeys(
        store,
        InMemoryAgentKeyStore(),
        BcryptSecretHasher(),
        AddAgentHandler(store, acl),
    )
    handles = InMemoryHandleStore()
    cache = SessionCache()
    resolve = ResolveSessionHandler(credentials, handles, cache)
    lease_store = InMemoryLeaseStore()
    factory = FakeFactory()
    counter = FixedCounter()
    log_in = LogInHandler(credentials, handles, cast(MailClientFactory, factory), cache)
    log_out = LogOutHandler(resolve, handles)
    service = LeaseService(
        lease_store, keys, credentials, log_in, resolve, log_out, counter
    )
    anyio.run(credentials.init_accounts, False)
    key = anyio.run(keys.add, "cursor")
    return Parts(
        leases=service,
        credentials=credentials,
        keys=keys,
        lease_store=lease_store,
        key=key,
        sessions=SessionService(log_in, resolve, log_out),
        factory=factory,
        counter=counter,
        log_in=log_in,
        resolve=resolve,
    )


def make_leases(tmp_path):
    parts = make_parts(tmp_path)
    return parts.leases, parts.key, parts.lease_store


def test_a_lease_gives_a_free_session_and_a_heartbeat(tmp_path):
    service, key, store = make_leases(tmp_path)

    first = anyio.run(service.acquire, key, "claude", "/work/a", 100)
    assert is_token(first["lease"])
    assert first["session"] == "cursor.s1"
    assert first["heartbeat_seconds"] == 30
    assert first["expires_seconds"] == 90
    second = anyio.run(service.acquire, key, "cursor", "/work/b", 200)
    assert second["session"] == "cursor.s2"

    beat = anyio.run(service.heartbeat, first["lease"])
    assert beat == {"session": "cursor.s1", "new_mail": 3}
    state = anyio.run(service.state, first["lease"])
    assert state.login == "cursor.s1"

    leases = anyio.run(service.leases)
    assert [(row["session"], row["app"], row["pid"]) for row in leases] == [
        ("cursor.s1", "claude", 100),
        ("cursor.s2", "cursor", 200),
    ]
    stored = anyio.run(store.all)
    assert all(first["lease"] not in lease.token_hash for lease in stored)

    anyio.run(service.end, first["lease"])
    with pytest.raises(LeaseError) as info:
        anyio.run(service.heartbeat, first["lease"])
    assert info.value.code == "LEASE_UNKNOWN"
    third = anyio.run(service.acquire, key, "claude", "/work/c", 300)
    assert third["session"] == "cursor.s1"


def test_a_lease_expires_90_seconds_after_the_last_heartbeat(tmp_path):
    service, key, _store = make_leases(tmp_path)
    start = dt.datetime(2026, 11, 12, 12, 0, tzinfo=dt.UTC)
    service.now = lambda: start
    lease = anyio.run(service.acquire, key, "claude", "/w", 1)["lease"]
    service.now = lambda: start + dt.timedelta(seconds=60)
    anyio.run(service.heartbeat, lease)
    service.now = lambda: start + dt.timedelta(seconds=149)
    anyio.run(service.heartbeat, lease)
    service.now = lambda: start + dt.timedelta(seconds=240)
    with pytest.raises(LeaseError) as info:
        anyio.run(service.heartbeat, lease)
    assert info.value.code == "LEASE_UNKNOWN"


def test_bad_key_a_full_pool_and_owner_end(tmp_path):
    service, key, _store = make_leases(tmp_path)
    with pytest.raises(LeaseError) as info:
        anyio.run(service.acquire, "mai1:cursor:" + "a" * 52, "x", "/w", 1)
    assert info.value.code == "BAD_KEY"
    for pid in range(8):
        anyio.run(service.acquire, key, "claude", "/w", pid)
    # A full pool gives a lease with no session.
    ninth = anyio.run(service.acquire, key, "claude", "/w", 9)
    assert is_token(ninth["lease"]) and ninth["session"] is None
    assert anyio.run(service.end_session, "cursor.s4") is True
    assert anyio.run(service.end_session, "cursor.s4") is False
    again = anyio.run(service.acquire, key, "claude", "/w", 10)
    assert again["session"] == "cursor.s4"
    with pytest.raises(LeaseError):
        anyio.run(service.heartbeat, "not-a-lease")


def test_a_named_lease_gives_the_session_by_label_or_by_login(tmp_path):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    anyio.run(parts.credentials.session_new, "cursor", "lane-b")

    first = anyio.run(
        parts.leases.acquire, parts.key, "cursor", "/work/a", 100, "lane-a"
    )
    assert is_token(first["lease"]) and first["session"] == "cursor.lane-a"
    assert set(first) == {"lease", "session", "heartbeat_seconds", "expires_seconds"}
    state = anyio.run(parts.leases.state, first["lease"])
    assert state.login == "cursor.lane-a"
    second = anyio.run(
        parts.leases.acquire, parts.key, "cursor", "/work/b", 200, "cursor.lane-b"
    )
    assert second["session"] == "cursor.lane-b"
    anyio.run(parts.leases.end, first["lease"])
    again = anyio.run(
        parts.leases.acquire, parts.key, "cursor", "/work/c", 300, "lane-a"
    )
    assert again["session"] == "cursor.lane-a"


def test_a_named_lease_refuses_a_busy_unknown_revoked_or_bad_session(tmp_path):
    parts = make_parts(tmp_path)
    codex = anyio.run(parts.keys.add, "codex")
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    anyio.run(parts.credentials.session_new, "cursor", "lane-b")
    anyio.run(parts.credentials.session_revoke, "cursor.lane-b")
    anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1, "lane-a")

    def code(key, session):
        with pytest.raises(LeaseError) as info:
            anyio.run(parts.leases.acquire, key, "cursor", "/w", 2, session)
        return info.value.code

    assert code(parts.key, "lane-a") == "SESSION_BUSY"
    assert code(parts.key, "cursor.lane-a") == "SESSION_BUSY"
    assert code(parts.key, "lane-b") == "SESSION_REVOKED"
    assert code(parts.key, "lane-c") == "SESSION_UNKNOWN"
    # A session of a different agent is unknown, also when it exists.
    assert code(codex, "cursor.lane-a") == "SESSION_UNKNOWN"
    assert code(codex, "lane-a") == "SESSION_UNKNOWN"
    for bad in ("", "Lane-A", "cursor.", "cursor.lane.a", "-x", "a" * 33):
        assert code(parts.key, bad) == "BAD_LABEL", bad
    assert code("mai1:cursor:" + "a" * 52, "lane-a") == "BAD_KEY"


def test_a_pool_lease_never_gives_a_named_session(tmp_path):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)["session"]
        for pid in range(8)
    ]
    assert pool == [f"cursor.s{n}" for n in range(1, 9)]
    ninth = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    assert ninth["session"] is None
    named = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 10, "lane-a")
    assert named["session"] == "cursor.lane-a"


def test_a_lease_request_with_a_pool_label_never_takes_a_pool_session(tmp_path):
    parts = make_parts(tmp_path)
    pool = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    assert pool["session"] == "cursor.s1"
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 2, "s1")
    assert info.value.code == "BAD_LABEL"
    anyio.run(parts.leases.end, pool["lease"])
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 3, "cursor.s1")
    assert info.value.code == "BAD_LABEL"
    next_pool = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 4)
    assert next_pool["session"] == "cursor.s1"


def test_an_expired_lease_frees_its_named_session(tmp_path):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    start = dt.datetime(2026, 11, 12, 12, 0, tzinfo=dt.UTC)
    parts.leases.now = lambda: start
    anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1, "lane-a")
    parts.leases.now = lambda: start + dt.timedelta(seconds=91)
    later = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 2, "lane-a")
    assert later["session"] == "cursor.lane-a"
