"""A pool lease when the pool of the agent is full: a lease with no session, and its bind."""

import datetime as dt

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.application.owner_sessions import OwnerSessions
from mai.modules.sessions.domain.lease import token_hash
from mai.platform.contract import ToolError
from tests.modules.sessions.test_lease_service import Parts, make_parts

# The contract limit of the leases with no session for each agent.
UNBOUND_LIMIT = 16


def fill_pool(parts: Parts) -> list[dict]:
    """Take the 8 pool sessions of cursor. Returns the 8 leases."""
    return [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(8)
    ]


def code_of(fn, *args) -> str:
    with pytest.raises(LeaseError) as info:
        anyio.run(fn, *args)
    return info.value.code


def test_a_full_pool_gives_a_lease_with_no_session_and_its_heartbeat(tmp_path):
    parts = make_parts(tmp_path)
    fill_pool(parts)
    extra = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w/x", 9)
    assert extra["session"] is None
    assert set(extra) == {"lease", "session", "heartbeat_seconds", "expires_seconds"}
    assert (extra["heartbeat_seconds"], extra["expires_seconds"]) == (30, 90)
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": None, "new_mail": 0}

    listed = anyio.run(parts.leases.leases)
    assert len(listed) == 9
    assert listed[-1]["session"] is None and listed[-1]["pid"] == 9
    assert listed[-1]["agent"] == "cursor" and listed[-1]["folder"] == "/w/x"
    assert anyio.run(parts.leases.lease_count, "cursor") == 9
    owner = OwnerSessions(parts.credentials, parts.leases)
    rows = anyio.run(owner.with_leases)
    assert sorted(row["login"] for row in rows) == [f"cursor.s{n}" for n in range(1, 9)]
    assert all(row["leased"] for row in rows)


def test_at_most_16_leases_with_no_session_for_each_agent(tmp_path):
    parts = make_parts(tmp_path)
    codex = anyio.run(parts.keys.add, "codex")
    fill_pool(parts)
    for pid in range(UNBOUND_LIMIT):
        lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 100 + pid)
        assert lease["session"] is None
    assert code_of(parts.leases.acquire, parts.key, "claude", "/w", 200) == (
        "NO_FREE_SESSION"
    )
    # The limit is for each agent: the pool of codex is free.
    other = anyio.run(parts.leases.acquire, codex, "codex", "/w", 300)
    assert other["session"] == "codex.s1"


def test_the_owner_end_rotate_and_revoke_never_touch_a_lease_with_no_session(
    tmp_path,
):
    parts = make_parts(tmp_path)
    owner = OwnerSessions(parts.credentials, parts.leases)
    fill_pool(parts)
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    assert anyio.run(owner.end_lease, "cursor.s1") is True
    anyio.run(owner.rotate, "cursor.s2")
    anyio.run(owner.revoke, "cursor.s3")
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": None, "new_mail": 0}


def test_a_lease_with_no_session_expires_and_ends(tmp_path):
    parts = make_parts(tmp_path)
    fill_pool(parts)
    start = parts.leases.now()
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    other = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 10)
    anyio.run(parts.leases.end, other["lease"])
    assert code_of(parts.leases.heartbeat, other["lease"]) == "LEASE_UNKNOWN"
    parts.leases.now = lambda: start + dt.timedelta(seconds=91)
    assert code_of(parts.leases.heartbeat, extra["lease"]) == "LEASE_UNKNOWN"
    assert anyio.run(parts.leases.leases) == []


def test_a_tool_state_binds_a_free_pool_session_one_time(tmp_path):
    parts = make_parts(tmp_path)
    pool = fill_pool(parts)
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    with pytest.raises(ToolError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "NO_FREE_SESSION"
    assert info.value.structured()["error"]["retry_safe"] is True
    # The failed bind keeps the lease, with no session.
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": None, "new_mail": 0}

    anyio.run(parts.leases.end, pool[3]["lease"])
    logins = len(parts.factory.opened)
    states = []

    async def two_calls():
        async def one():
            states.append(await parts.leases.state(extra["lease"]))

        async with anyio.create_task_group() as group:
            group.start_soon(one)
            group.start_soon(one)

    anyio.run(two_calls)
    assert [state.login for state in states] == ["cursor.s4", "cursor.s4"]
    assert states[0].handle == states[1].handle
    assert len(parts.factory.opened) == logins + 1
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": "cursor.s4", "new_mail": 3}
    rows = {row["session"]: row for row in anyio.run(parts.leases.leases)}
    assert rows["cursor.s4"]["pid"] == 9 and None not in rows
    # The owner end of the session cursor.s4 now ends the lease of the bind.
    assert anyio.run(parts.leases.end_session, "cursor.s4") is True
    assert code_of(parts.leases.heartbeat, extra["lease"]) == "LEASE_UNKNOWN"


def test_a_bind_logs_out_when_the_lease_ends_during_the_mail_login(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    pool = fill_pool(parts)
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    anyio.run(parts.leases.end, pool[0]["lease"])
    log_in = parts.log_in.execute

    async def slow_log_in(command):
        state = await log_in(command)
        await parts.leases.end(extra["lease"])
        return state

    monkeypatch.setattr(parts.log_in, "execute", slow_log_in)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "LEASE_UNKNOWN"
    assert anyio.run(parts.lease_store.get, token_hash(extra["lease"])) is None
    assert parts.factory.opened[-1].closed
    monkeypatch.undo()
    # The session of the failed bind is free again.
    again = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 10)
    assert again["session"] == "cursor.s1"


def test_a_heartbeat_does_not_undo_a_bind(tmp_path, monkeypatch):
    parts = make_parts(tmp_path)
    pool = fill_pool(parts)
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    anyio.run(parts.leases.end, pool[0]["lease"])
    log_in = parts.log_in.execute
    beats = []

    async def log_in_with_a_heartbeat(command):
        state = await log_in(command)
        # A heartbeat of the same lease occurs while the bind waits for the mail server.
        beats.append(await parts.leases.heartbeat(extra["lease"]))
        return state

    monkeypatch.setattr(parts.log_in, "execute", log_in_with_a_heartbeat)
    state = anyio.run(parts.leases.state, extra["lease"])
    assert state.login == "cursor.s1"
    assert beats == [{"session": None, "new_mail": 0}]
    stored = anyio.run(parts.lease_store.get, token_hash(extra["lease"]))
    assert stored is not None and stored.session == "cursor.s1"
