"""The owner operations on the sessions, on the memory stores with a fake mail server."""

import json

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.application.owner_sessions import OwnerSessions
from tests.modules.sessions.test_lease_service import make_parts


def make_owner(tmp_path):
    parts = make_parts(tmp_path)
    return OwnerSessions(parts.credentials, parts.leases), parts


def code_of(fn, *args) -> str:
    with pytest.raises(LeaseError) as info:
        anyio.run(fn, *args)
    return info.value.code


def assert_no_secret(answer) -> None:
    text = json.dumps(answer)
    assert "mai1:" not in text and "credential" not in text and "secret" not in text


def test_new_makes_a_named_session_and_gives_no_secret(tmp_path):
    owner, parts = make_owner(tmp_path)
    made = anyio.run(owner.new, "cursor", "lane-a")
    assert made == {
        "login": "cursor.lane-a",
        "agent": "cursor",
        "label": "lane-a",
        "status": "active",
    }
    assert code_of(owner.new, "cursor", "lane-a") == "SESSION_EXISTS"
    assert code_of(owner.new, "cursor", "Lane A") == "BAD_LABEL"
    assert code_of(owner.new, "cursor", 7) == "BAD_LABEL"
    assert code_of(owner.new, "nobody", "lane-a") == "AGENT_UNKNOWN"
    anyio.run(parts.keys.add, "codex")
    anyio.run(parts.keys.remove, "codex")
    assert code_of(owner.new, "codex", "lane-a") == "AGENT_UNKNOWN"
    # A named session has no expiry: only the owner closes it.
    [row] = anyio.run(parts.credentials.session_list, "cursor")
    assert row["expires_at"] > "2100"


def test_new_pool_makes_each_pool_session_that_does_not_exist(tmp_path):
    owner, parts = make_owner(tmp_path)
    anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    made = anyio.run(owner.new_pool, "cursor", 3)
    assert [row["login"] for row in made] == ["cursor.s2", "cursor.s3"]
    assert all(row["status"] == "active" for row in made)
    assert anyio.run(owner.new_pool, "cursor", 3) == []
    for bad in (0, 9, True, "3", None):
        assert code_of(owner.new_pool, "cursor", bad) == "BAD_REQUEST"
    assert code_of(owner.new_pool, "nobody", 2) == "AGENT_UNKNOWN"


def test_with_leases_gives_the_status_and_the_lease_of_each_session(tmp_path):
    owner, parts = make_owner(tmp_path)
    anyio.run(owner.new, "cursor", "lane-a")
    anyio.run(owner.new, "cursor", "lane-b")
    anyio.run(owner.revoke, "cursor.lane-b")
    anyio.run(parts.leases.acquire, parts.key, "cursor", "/work/a", 42, "lane-a")
    rows = anyio.run(owner.with_leases)
    assert_no_secret(rows)
    by_login = {row["login"]: row for row in rows}
    lane_a = by_login["cursor.lane-a"]
    assert set(lane_a) == {
        "login",
        "agent",
        "label",
        "status",
        "leased",
        "app",
        "folder",
        "pid",
        "last_seen_at",
    }
    assert (lane_a["status"], lane_a["leased"], lane_a["app"], lane_a["pid"]) == (
        "active",
        True,
        "cursor",
        42,
    )
    assert lane_a["folder"] == "/work/a" and lane_a["last_seen_at"]
    lane_b = by_login["cursor.lane-b"]
    assert (lane_b["status"], lane_b["leased"], lane_b["app"]) == (
        "revoked",
        False,
        None,
    )
    records = anyio.run(owner.records, "cursor")
    assert [row["login"] for row in records] == ["cursor.lane-a", "cursor.lane-b"]
    assert_no_secret(records)


def test_rotate_ends_the_lease_and_keeps_the_session_open(tmp_path):
    owner, parts = make_owner(tmp_path)
    anyio.run(owner.new, "cursor", "lane-a")
    lease = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1, "lane-a")[
        "lease"
    ]
    rotated = anyio.run(owner.rotate, "cursor.lane-a")
    assert rotated == {"login": "cursor.lane-a", "status": "active"}
    assert code_of(parts.leases.heartbeat, lease) == "LEASE_UNKNOWN"
    again = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 2, "lane-a")
    assert again["session"] == "cursor.lane-a"
    assert code_of(owner.rotate, "cursor.lane-z") == "SESSION_UNKNOWN"
    assert code_of(owner.rotate, "cursor") == "SESSION_UNKNOWN"


def test_revoke_ends_the_lease_and_each_later_lease_is_refused(tmp_path):
    owner, parts = make_owner(tmp_path)
    anyio.run(owner.new, "cursor", "lane-b")
    lease = anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1, "lane-b")[
        "lease"
    ]
    revoked = anyio.run(owner.revoke, "cursor.lane-b")
    assert revoked == {"login": "cursor.lane-b", "status": "revoked"}
    assert code_of(parts.leases.heartbeat, lease) == "LEASE_UNKNOWN"
    assert (
        code_of(parts.leases.acquire, parts.key, "cursor", "/w", 2, "lane-b")
        == "SESSION_REVOKED"
    )
    assert code_of(owner.rotate, "cursor.lane-b") == "SESSION_REVOKED"
    assert anyio.run(owner.revoke, "cursor.lane-b") == revoked
    assert code_of(owner.revoke, "cursor.none") == "SESSION_UNKNOWN"


def test_end_lease_ends_only_a_live_lease(tmp_path):
    owner, parts = make_owner(tmp_path)
    anyio.run(owner.new, "cursor", "lane-a")
    assert anyio.run(owner.end_lease, "cursor.lane-a") is False
    anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1, "lane-a")
    assert anyio.run(owner.end_lease, "cursor.lane-a") is True
    assert anyio.run(owner.end_lease, "cursor.lane-a") is False
