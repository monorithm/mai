"""A lease with no session ends when the owner removes its agent."""

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.domain.lease import MAX_SESSIONS, token_hash
from tests.modules.sessions.test_lease_service import Parts, make_parts
from tests.modules.sessions.test_sessions_api import AUTH, api, lease

__all__ = ["api"]


def heartbeat(client, token):
    return client.post(
        "/api/leases/current/heartbeat", headers={"authorization": f"Bearer {token}"}
    )


def full_pool(parts: Parts) -> tuple[list[dict], dict]:
    """The 8 pool leases of cursor, and a 9th lease with no session."""
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    assert extra["session"] is None
    return pool, extra


def test_a_lease_with_no_session_ends_when_the_owner_removes_the_agent(api):
    client, key = api
    pool = [lease(client, key, pid=pid).json() for pid in range(MAX_SESSIONS)]
    extra = lease(client, key, pid=99).json()
    assert extra["session"] is None
    removed = client.delete("/api/agents/cursor", headers=AUTH)
    assert removed.status_code == 204

    # A lease on a pool session ends: the bridge renews and gets BAD_KEY.
    bound = heartbeat(client, pool[0]["lease"])
    assert bound.status_code == 401
    assert bound.json()["error"]["code"] == "LEASE_UNKNOWN"
    assert lease(client, key, pid=100).json()["error"]["code"] == "BAD_KEY"

    # The lease with no session of the removed agent also ends. Thus the
    # bridge of the removed agent renews and gets BAD_KEY.
    unbound = heartbeat(client, extra["lease"])
    assert unbound.status_code == 401, unbound.json()
    assert unbound.json()["error"]["code"] == "LEASE_UNKNOWN"
    listed = client.get("/api/leases", headers=AUTH).json()["leases"]
    assert all(row["session"] is not None for row in listed), listed


def test_a_tool_call_on_a_lease_with_no_session_of_a_removed_agent_is_not_retry_safe(
    tmp_path,
):
    parts = make_parts(tmp_path)
    _pool, extra = full_pool(parts)
    sessions = len(anyio.run(parts.credentials.session_list, "cursor"))
    anyio.run(parts.keys.remove, "cursor")
    # NO_FREE_SESSION with retry_safe true tells the chat to try again. For a
    # removed agent, no later call can succeed: the lease is not valid.
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "LEASE_UNKNOWN"
    assert anyio.run(parts.lease_store.get, token_hash(extra["lease"])) is None
    # The bind made no pool session for the removed agent.
    assert len(anyio.run(parts.credentials.session_list, "cursor")) == sessions


def test_a_store_failure_of_the_agent_check_keeps_the_lease_with_no_session(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    _pool, extra = full_pool(parts)

    async def is_active(name):
        raise RuntimeError("the database does not reply")

    monkeypatch.setattr(parts.keys, "is_active", is_active)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.heartbeat, extra["lease"])
    error = info.value
    assert (error.code, error.status, error.retry_safe) == (
        "STORE_UNAVAILABLE",
        503,
        True,
    )
    monkeypatch.undo()
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": None, "new_mail": 0}
