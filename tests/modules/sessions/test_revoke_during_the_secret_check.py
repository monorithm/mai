"""An owner revoke that commits during the check of the secret of a lease request.

AuthenticateHandler reads the principal, checks the secret and the status,
and then marks the login (CredentialStore.mark_login). In the server each
step is a PostgreSQL call, thus an owner revoke can commit between the read
and the mark. A revoke during the lease request gives SESSION_REVOKED, and a revoked pool
session never gets a lease again.
"""

import anyio

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.application.owner_sessions import OwnerSessions
from tests.modules.sessions.test_lease_service import make_parts


def test_a_revoke_during_the_secret_check_of_a_pool_lease_request(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    owner = OwnerSessions(parts.credentials, parts.leases)
    first = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    anyio.run(parts.leases.end, first["lease"])
    assert first["session"] == "cursor.s1"
    store = parts.keys._store
    mark_login = store.mark_login
    done = []

    async def revoke_then_mark(login, now):
        if login == "cursor.s1" and not done:
            done.append(login)
            # The revoke of the owner commits after the read of the principal.
            await parts.credentials.session_revoke(login)
        return await mark_login(login, now)

    monkeypatch.setattr(store, "mark_login", revoke_then_mark)
    try:
        result = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 2)
    except LeaseError as exc:
        result = exc.code
    monkeypatch.setattr(store, "mark_login", mark_login)
    # The rest of the owner revoke: the end of the lease of the session.
    anyio.run(owner.end_lease, "cursor.s1")
    rows = {
        r["login"]: r["status"]
        for r in anyio.run(parts.credentials.session_list, "cursor")
    }
    again = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 3)
    session = result["session"] if isinstance(result, dict) else result
    assert done == ["cursor.s1"]
    assert rows["cursor.s1"] == "revoked", (session, rows, again["session"])
    assert again["session"] != "cursor.s1", (session, rows, again["session"])
