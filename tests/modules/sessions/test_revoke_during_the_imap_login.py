"""A revoke that comes during the IMAP login of a lease request or a bind.

The mail login checks the secret in the store, then connects to Dovecot. A
revoke between the two closes the session in the store, and Dovecot then
refuses the login with LOGIN_FAILED. A revoke during the lease request gives
SESSION_REVOKED, and the pool tries the next free pool session.
"""

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from mai.platform.contract import ToolError
from tests.modules.sessions.test_lease_service import Parts, make_parts


class RevokedAtConnect:
    """A mail session like MailSession: Dovecot refuses the login of a revoked session."""

    def __init__(self, parts: Parts, login: str, revoke: bool):
        self.login = login
        self._parts = parts
        self._revoke = revoke
        self.closed = False

    def connect(self):
        if self._revoke:
            # The owner revokes the session while the connection opens.
            anyio.from_thread.run(self._parts.credentials.session_revoke, self.login)
            raise ToolError("LOGIN_FAILED")
        return self

    def close(self):
        self.closed = True


def revoke_at_the_first_connect(parts: Parts, monkeypatch) -> list[str]:
    revoked: list[str] = []

    def open_mail(login, secret):
        first = not revoked
        if first:
            revoked.append(login)
        return RevokedAtConnect(parts, login, first)

    monkeypatch.setattr(parts.factory, "open", open_mail)
    return revoked


def test_a_pool_lease_request_takes_a_different_free_session_when_a_revoke_comes_during_the_imap_login(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    revoked = revoke_at_the_first_connect(parts, monkeypatch)
    try:
        lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    except LeaseError as exc:
        pytest.fail(
            f"the pool lease request gave {exc.status} {exc.code} retry_safe "
            f"{exc.retry_safe}, but cursor.s2 can be made (revoked: {revoked})"
        )
    assert revoked == ["cursor.s1"]
    assert lease["session"] == "cursor.s2"


def test_a_named_lease_request_gives_session_revoked_when_a_revoke_comes_during_the_imap_login(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    revoked = revoke_at_the_first_connect(parts, monkeypatch)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1, "lane-a")
    assert revoked == ["cursor.lane-a"]
    assert (info.value.code, info.value.status) == ("SESSION_REVOKED", None), (
        info.value.status,
        info.value.code,
    )


def test_a_bind_takes_a_different_free_session_when_a_revoke_comes_during_the_imap_login(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    anyio.run(parts.leases.end, pool[3]["lease"])
    anyio.run(parts.leases.end, pool[4]["lease"])
    revoked = revoke_at_the_first_connect(parts, monkeypatch)
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
