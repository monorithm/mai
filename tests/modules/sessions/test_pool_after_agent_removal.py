"""A pool lease request or a bind at the time of an agent removal.

The owner removal of an agent revokes each open session of the agent and the
agent itself. It does not wait for the lease lock. A lease request or a bind
that runs at that time must not make a new pool session for the removed
agent, and the lease must end at its next heartbeat or tool call.
"""

import anyio

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from tests.modules.sessions.test_lease_service import Parts, make_parts


def remove_the_agent_at_the_first_mail_login(parts: Parts, monkeypatch) -> list[str]:
    """The owner removes the agent cursor just before the first mail login."""
    log_in = parts.log_in.execute
    seen: list[str] = []

    async def remove_then_log_in(command):
        login, _secret = parts.credentials.parse_credential(command.credential)
        seen.append(login)
        if len(seen) == 1:
            await parts.keys.remove("cursor")
        return await log_in(command)

    monkeypatch.setattr(parts.log_in, "execute", remove_then_log_in)
    return seen


def open_sessions(parts: Parts) -> list[str]:
    rows = anyio.run(parts.credentials.session_list, "cursor")
    return [r["login"] for r in rows if r.get("status") in ("issued", "active")]


def test_a_pool_lease_request_during_an_agent_removal_makes_no_new_session(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    seen = remove_the_agent_at_the_first_mail_login(parts, monkeypatch)
    try:
        lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    except LeaseError:
        lease = None
    if lease is not None and lease["session"] is not None:
        # The contract: a heartbeat after the removal gives LEASE_UNKNOWN.
        try:
            beat = anyio.run(parts.leases.heartbeat, lease["lease"])
        except LeaseError as exc:
            beat = exc.code
        assert beat == "LEASE_UNKNOWN", (seen, lease["session"], beat)
    # The owner removed the agent: no session of the agent may stay open.
    assert open_sessions(parts) == [], (seen, lease)


def test_a_bind_during_an_agent_removal_makes_no_new_session(tmp_path, monkeypatch):
    parts = make_parts(tmp_path)
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    assert extra["session"] is None
    anyio.run(parts.leases.end, pool[3]["lease"])
    seen = remove_the_agent_at_the_first_mail_login(parts, monkeypatch)
    try:
        state = anyio.run(parts.leases.state, extra["lease"])
        bound = state.login
    except Exception as exc:  # noqa: BLE001 - any refusal is correct here
        bound = type(exc).__name__
    # The contract: a heartbeat after the removal gives LEASE_UNKNOWN.
    try:
        beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    except LeaseError as exc:
        beat = exc.code
    assert beat == "LEASE_UNKNOWN", (seen, bound, beat)
    assert open_sessions(parts) == [], (seen, bound)
