"""The code of a time limit is the code of the part that runs.

The mail login also uses the store: it checks the secret and adds the handle.
When only the store does not reply, the part that runs is the store, thus the
code is STORE_UNAVAILABLE, not MAIL_SERVER_DOWN. When the owner rotates the
selected pool session during a lease request, the pool tries one different
free session.
"""

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from tests.modules.sessions.test_lease_service import make_parts

NO_REPLY_SECONDS = 60
LIMIT = 0.3


def test_a_handle_store_that_does_not_reply_during_the_login_gives_store_unavailable(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    parts.leases.request_seconds = LIMIT
    handles = parts.log_in._handles

    async def silent_add(*args, **kwargs):
        await anyio.sleep(NO_REPLY_SECONDS)

    monkeypatch.setattr(handles, "add", silent_add)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    assert (info.value.status, info.value.code) == (503, "STORE_UNAVAILABLE")


def test_a_credential_store_that_does_not_reply_at_the_secret_check_gives_store_unavailable(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    parts.leases.request_seconds = LIMIT

    async def silent_authenticate(*args, **kwargs):
        await anyio.sleep(NO_REPLY_SECONDS)

    monkeypatch.setattr(parts.credentials, "authenticate", silent_authenticate)
    with pytest.raises(LeaseError) as info:
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    assert (info.value.status, info.value.code) == (503, "STORE_UNAVAILABLE")


def test_a_pool_lease_request_takes_a_different_free_session_when_the_owner_rotates_the_selected_one(
    tmp_path, monkeypatch
):
    """The owner rotates the selected pool session after its new secret, before its mail login."""
    parts = make_parts(tmp_path)
    log_in = parts.log_in.execute
    rotated: list[str] = []

    async def rotate_then_log_in(command):
        if not rotated:
            login, _secret = parts.credentials.parse_credential(command.credential)
            rotated.append(login)
            await parts.credentials.session_rotate(login)
        return await log_in(command)

    monkeypatch.setattr(parts.log_in, "execute", rotate_then_log_in)
    try:
        lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    except LeaseError as exc:
        pytest.fail(
            f"the pool lease request gave {exc.status} {exc.code} retry_safe "
            f"{exc.retry_safe}, but a pool session is free (rotated: {rotated})"
        )
    assert lease["session"] is not None
