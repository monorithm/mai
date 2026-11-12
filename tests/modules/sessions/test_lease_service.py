"""The leases of the sessions on the memory stores with a fake mail server."""

import datetime as dt
from typing import cast

import anyio
import pytest

from mai.modules.credentials.application.add_agent_command import AddAgentHandler
from mai.modules.credentials.application.agent_keys import AgentKeys
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
from mai.modules.sessions.domain.lease import is_token
from mai.modules.sessions.domain.new_mail_counter import NewMailCounter
from mai.modules.sessions.infra.in_memory_handle_store import InMemoryHandleStore
from mai.modules.sessions.infra.in_memory_lease_store import InMemoryLeaseStore
from mai.platform.mailserver.mail_client_factory import MailClientFactory
from tests.modules.credentials.test_credential_service import make_service
from tests.modules.sessions.test_session_tools import FakeFactory


class FixedCounter(NewMailCounter):
    def __init__(self) -> None:
        self.value = 3

    async def count(self, state) -> int:
        return self.value


def make_leases(tmp_path):
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
    service = LeaseService(
        lease_store,
        keys,
        credentials,
        LogInHandler(
            credentials, handles, cast(MailClientFactory, FakeFactory()), cache
        ),
        resolve,
        LogOutHandler(resolve, handles),
        FixedCounter(),
    )
    anyio.run(credentials.init_accounts, False)
    key = anyio.run(keys.add, "cursor")
    return service, key, lease_store


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
    sessions = anyio.run(service.sessions)
    assert sessions == [
        {"login": "cursor.s1", "agent": "cursor", "leased": True},
        {"login": "cursor.s2", "agent": "cursor", "leased": True},
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


def test_bad_key_no_free_session_and_owner_end(tmp_path):
    service, key, _store = make_leases(tmp_path)
    with pytest.raises(LeaseError) as info:
        anyio.run(service.acquire, "mai1:cursor:" + "a" * 52, "x", "/w", 1)
    assert info.value.code == "BAD_KEY"
    for pid in range(8):
        anyio.run(service.acquire, key, "claude", "/w", pid)
    with pytest.raises(LeaseError) as info:
        anyio.run(service.acquire, key, "claude", "/w", 9)
    assert info.value.code == "NO_FREE_SESSION"
    assert anyio.run(service.end_session, "cursor.s4") is True
    assert anyio.run(service.end_session, "cursor.s4") is False
    again = anyio.run(service.acquire, key, "claude", "/w", 10)
    assert again["session"] == "cursor.s4"
    with pytest.raises(LeaseError):
        anyio.run(service.heartbeat, "not-a-lease")
