"""A revoke that closes the selected pool session during a bind or a pool lease request."""

import anyio
import pytest

from mai.modules.credentials import AccountStateError
from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from mai.platform.contract import ToolError
from tests.modules.sessions.test_lease_service import Parts, make_parts


def full_pool(parts: Parts) -> tuple[list[dict], dict]:
    """The 8 pool leases of cursor, and a 9th lease with no session."""
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    assert extra["session"] is None
    return pool, extra


def revoke_first_rotate(parts: Parts, monkeypatch) -> list[str]:
    """Make the first rotate revoke its session first. Returns the revoked logins."""
    rotate = parts.credentials.session_rotate
    revoked: list[str] = []

    async def revoke_then_rotate(login, file=None):
        if not revoked:
            # The owner revokes the session after the read of the session list.
            revoked.append(login)
            await parts.credentials.session_revoke(login)
        return await rotate(login, file)

    monkeypatch.setattr(parts.credentials, "session_rotate", revoke_then_rotate)
    return revoked


def test_a_bind_takes_a_different_free_pool_session_when_a_revoke_wins_the_race(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    pool, extra = full_pool(parts)
    # cursor.s4 and cursor.s5 are free.
    anyio.run(parts.leases.end, pool[3]["lease"])
    anyio.run(parts.leases.end, pool[4]["lease"])
    revoked = revoke_first_rotate(parts, monkeypatch)
    try:
        state = anyio.run(parts.leases.state, extra["lease"])
    except ToolError as exc:
        pytest.fail(
            f"the bind gave {exc.code}, but cursor.s5 is free (revoked: {revoked})"
        )
    assert revoked == ["cursor.s4"]
    assert state.login == "cursor.s5"
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat["session"] == "cursor.s5"


def test_a_bind_gives_no_free_session_when_the_revoke_closes_the_only_free_session(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    pool, extra = full_pool(parts)
    anyio.run(parts.leases.end, pool[3]["lease"])
    revoked = revoke_first_rotate(parts, monkeypatch)
    with pytest.raises(ToolError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "NO_FREE_SESSION"
    assert revoked == ["cursor.s4"]
    beat = anyio.run(parts.leases.heartbeat, extra["lease"])
    assert beat == {"session": None, "new_mail": 0}


def test_a_pool_lease_request_takes_a_different_free_pool_session_when_a_revoke_wins_the_race(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    revoked = revoke_first_rotate(parts, monkeypatch)
    pool = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    assert revoked == ["cursor.s1"]
    assert pool["session"] == "cursor.s2"


def test_an_open_failure_of_a_session_that_stays_open_tries_no_other_session(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    pool, extra = full_pool(parts)
    anyio.run(parts.leases.end, pool[3]["lease"])
    anyio.run(parts.leases.end, pool[4]["lease"])
    tried = []

    async def rotate(login, file=None):
        # The store refuses the rotate, but the session list tells that it is open.
        tried.append(login)
        raise AccountStateError(f"session is revoked: {login}")

    monkeypatch.setattr(parts.credentials, "session_rotate", rotate)
    with pytest.raises(ToolError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "NO_FREE_SESSION"
    assert tried == ["cursor.s4"]


def test_a_pool_lease_request_makes_one_pool_session_when_its_open_fails(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)

    async def rotate(login, file=None):
        raise AccountStateError(f"session is revoked: {login}")

    monkeypatch.setattr(parts.credentials, "session_rotate", rotate)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    assert info.value.code == "SESSION_REVOKED"
    logins = [row["login"] for row in anyio.run(parts.credentials.session_list)]
    assert logins == ["cursor.s1"]
