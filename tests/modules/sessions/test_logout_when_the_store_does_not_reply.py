"""The end of a lease when the handle store does not reply or fails.

The logout of the handle of a lease first resolves the handle in the store.
When the store does not reply or fails, the logout stops. The mail
connections of the handle must close also then: the lease is gone, and
nothing else closes them.
"""

import anyio

from mai.modules.sessions.application import lease_service
from tests.modules.sessions.test_lease_service import make_parts

NO_REPLY_SECONDS = 60


def test_the_end_of_a_lease_closes_its_mail_connections_when_the_store_does_not_reply(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    monkeypatch.setattr(lease_service, "LOGOUT_LIMIT_SECONDS", 0.3)
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    mail = parts.factory.opened[-1]
    handle = parts.lease_store._leases[next(iter(parts.lease_store._leases))].handle
    assert handle is not None
    handles = parts.log_in._handles

    async def silent_extend(*args, **kwargs):
        await anyio.sleep(NO_REPLY_SECONDS)

    monkeypatch.setattr(handles, "extend", silent_extend)
    # The app stops: the bridge ends the lease (DELETE /api/leases/current).
    anyio.run(parts.leases.end, lease["lease"])
    assert anyio.run(parts.leases.leases) == []
    cached = parts.resolve._cache.get(handle) is not None
    assert (mail.closed, cached) == (True, False)


def test_the_end_of_a_lease_closes_its_mail_connections_when_the_store_fails(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    mail = parts.factory.opened[-1]
    handle = parts.lease_store._leases[next(iter(parts.lease_store._leases))].handle
    assert handle is not None
    handles = parts.log_in._handles

    async def failed_extend(*args, **kwargs):
        raise OSError("connection refused")

    monkeypatch.setattr(handles, "extend", failed_extend)
    anyio.run(parts.leases.end, lease["lease"])
    assert anyio.run(parts.leases.leases) == []
    cached = parts.resolve._cache.get(handle) is not None
    assert (mail.closed, cached) == (True, False)
