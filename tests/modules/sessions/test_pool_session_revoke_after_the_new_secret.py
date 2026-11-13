"""A revoke that closes the selected pool session after its new secret, before its mail login.

The owner can revoke a pool session at each time during a bind or a pool lease
request: the revoke does not wait for the lease lock. When a different pool
session is free, the bind and the pool lease request must take it.
"""

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from mai.platform.contract import ToolError
from tests.modules.sessions.test_lease_service import Parts, make_parts


def revoke_at_the_first_mail_login(parts: Parts, monkeypatch) -> list[str]:
    """Make the owner revoke the session of the first mail login just before it. Returns the revoked logins."""
    log_in = parts.log_in.execute
    revoked: list[str] = []

    async def revoke_then_log_in(command):
        if not revoked:
            login, _secret = parts.credentials.parse_credential(command.credential)
            revoked.append(login)
            await parts.credentials.session_revoke(login)
        return await log_in(command)

    monkeypatch.setattr(parts.log_in, "execute", revoke_then_log_in)
    return revoked


def test_a_bind_takes_a_different_free_pool_session_when_a_revoke_comes_after_the_new_secret(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    assert extra["session"] is None
    # cursor.s4 and cursor.s5 are free.
    anyio.run(parts.leases.end, pool[3]["lease"])
    anyio.run(parts.leases.end, pool[4]["lease"])
    revoked = revoke_at_the_first_mail_login(parts, monkeypatch)
    try:
        state = anyio.run(parts.leases.state, extra["lease"])
    except ToolError as exc:
        pytest.fail(
            f"the bind gave {exc.code} (retry_safe "
            f"{exc.structured()['error']['retry_safe']}), but cursor.s5 is free "
            f"(revoked: {revoked})"
        )
    assert revoked == ["cursor.s4"]
    assert state.login == "cursor.s5"


def test_a_named_lease_request_gives_session_revoked_when_a_revoke_comes_after_the_new_secret(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    revoked = revoke_at_the_first_mail_login(parts, monkeypatch)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1, "lane-a")
    assert revoked == ["cursor.lane-a"]
    # The lease API tells a revoke during the lease request with 409 SESSION_REVOKED.
    assert (info.value.code, info.value.status) == ("SESSION_REVOKED", None), (
        info.value.status,
        info.value.code,
    )


def test_a_pool_lease_request_takes_a_different_free_pool_session_when_a_revoke_comes_after_the_new_secret(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    revoked = revoke_at_the_first_mail_login(parts, monkeypatch)
    try:
        lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    except LeaseError as exc:
        pytest.fail(
            f"the pool lease request gave {exc.status} {exc.code}, but cursor.s2 "
            f"can be made (revoked: {revoked})"
        )
    assert revoked == ["cursor.s1"]
    assert lease["session"] == "cursor.s2"
