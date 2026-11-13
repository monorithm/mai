"""An agent removal that runs while a pool lease request makes a new pool session.

The removal of an agent does not take the lease lock. It reads the session
list of the agent one time, revokes each open session in that list, and
then sets the agent to revoked. A lease request can run between these
steps. This test holds the last step of the removal in flight, as a slow
transaction does, while a pool lease request makes a new pool session. No
lease of the removed agent stays live after its next heartbeat or tool call.
"""

import anyio

from mai.modules.sessions.application.lease_service import LeaseError
from tests.modules.sessions.test_lease_service import make_parts


def _open_sessions(parts) -> list[str]:
    rows = anyio.run(parts.credentials.session_list, "cursor")
    return [r["login"] for r in rows if r.get("status") in ("issued", "active")]


def _race(parts, monkeypatch, run_request):
    store = parts.keys._store
    set_status = store.set_status
    results: dict = {}

    async def main():
        in_last_step = anyio.Event()
        go = anyio.Event()

        async def slow_set_status(login, status, now):
            if login == "cursor" and status == "revoked":
                # The last transaction of the removal is in flight.
                in_last_step.set()
                await go.wait()
            await set_status(login, status, now)

        monkeypatch.setattr(store, "set_status", slow_set_status)

        async def request():
            try:
                results["request"] = await run_request()
            except LeaseError as exc:
                results["request"] = exc.code

        async def remove():
            await parts.keys.remove("cursor")
            results["removed"] = True

        # A different lease operation holds the lease lock, thus the request
        # passes the key check and waits for the lock.
        await parts.leases._lock.acquire()
        async with anyio.create_task_group() as tg:
            tg.start_soon(request)
            await anyio.sleep(0.2)
            tg.start_soon(remove)
            await in_last_step.wait()
            parts.leases._lock.release()
            with anyio.move_on_after(5):
                while "request" not in results:
                    await anyio.sleep(0.01)
            go.set()

    anyio.run(main)
    monkeypatch.setattr(store, "set_status", set_status)
    return results


def test_a_pool_lease_request_during_the_last_step_of_an_agent_removal(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    results = _race(
        parts,
        monkeypatch,
        lambda: parts.leases.acquire(parts.key, "claude", "/w", 1),
    )
    assert results.get("removed") is True
    assert not anyio.run(parts.keys.is_active, "cursor")
    lease = results["request"]
    beat = None
    if isinstance(lease, dict):
        try:
            beat = anyio.run(parts.leases.heartbeat, lease["lease"])
        except LeaseError as exc:
            beat = exc.code
    session = lease.get("session") if isinstance(lease, dict) else lease
    # The lease of the removed agent must not stay live, and no session of the
    # removed agent may stay open.
    assert beat in (None, "LEASE_UNKNOWN"), (session, beat)
    assert _open_sessions(parts) == [], (session, beat)


def test_a_bind_during_the_last_step_of_an_agent_removal(tmp_path, monkeypatch):
    from mai.modules.sessions.application.owner_sessions import OwnerSessions
    from mai.modules.sessions.domain.lease import MAX_SESSIONS

    parts = make_parts(tmp_path)
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    assert extra["session"] is None
    # A revoke frees a place in the pool: the bind makes the pool session s9.
    owner = OwnerSessions(parts.credentials, parts.leases)
    anyio.run(owner.revoke, pool[0]["session"])

    async def bind():
        state = await parts.leases.state(extra["lease"])
        return {"lease": extra["lease"], "session": state.login}

    results = _race(parts, monkeypatch, bind)
    assert results.get("removed") is True
    assert not anyio.run(parts.keys.is_active, "cursor")
    try:
        beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    except LeaseError as exc:
        beat = exc.code
    bound = results["request"]
    assert beat == "LEASE_UNKNOWN", (bound, beat)
    assert _open_sessions(parts) == [], (bound, beat)
