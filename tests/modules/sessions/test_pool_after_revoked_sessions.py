"""The pool of an agent when some of its pool sessions are closed.

A revoke is final: a revoked pool session never gets a lease again. Thus a
closed pool session does not hold a place in the pool. The pool has a
maximum of MAX_SESSIONS open pool sessions, and the numbers of new pool
sessions go past s8.
"""

import anyio

from mai.modules.sessions.domain.lease import MAX_SESSIONS
from tests.modules.sessions.test_lease_service import make_parts


def test_an_agent_added_again_after_a_removal_gets_a_pool_session(tmp_path):
    parts = make_parts(tmp_path)
    leases = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    for lease in leases:
        anyio.run(parts.leases.end, lease["lease"])
    anyio.run(parts.keys.remove, "cursor")
    again = anyio.run(parts.keys.add, "cursor")
    lease = anyio.run(parts.leases.acquire, again, "claude", "/w", 99)
    assert lease["session"] == "cursor.s9"


def test_a_revoked_pool_session_gives_its_place_to_a_new_pool_session(tmp_path):
    parts = make_parts(tmp_path)
    leases = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    anyio.run(parts.credentials.session_revoke, "cursor.s3")
    anyio.run(parts.leases.end_session, "cursor.s3")
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 50)
    assert lease["session"] == "cursor.s9"
    # The pool has MAX_SESSIONS open pool sessions, and each has a lease.
    full = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 51)
    assert full["session"] is None
    assert len(leases) == MAX_SESSIONS


def test_a_free_open_pool_session_comes_before_a_new_pool_session(tmp_path):
    parts = make_parts(tmp_path)
    first = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    second = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 2)
    assert (first["session"], second["session"]) == ("cursor.s1", "cursor.s2")
    anyio.run(parts.credentials.session_revoke, "cursor.s1")
    anyio.run(parts.leases.end_session, "cursor.s1")
    anyio.run(parts.leases.end, second["lease"])
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 3)
    assert lease["session"] == "cursor.s2"
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 4)
    assert lease["session"] == "cursor.s3"


def test_a_named_session_does_not_hold_a_place_in_the_pool(tmp_path):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    leases = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    assert [lease["session"] for lease in leases] == [
        f"cursor.s{number}" for number in range(1, MAX_SESSIONS + 1)
    ]
