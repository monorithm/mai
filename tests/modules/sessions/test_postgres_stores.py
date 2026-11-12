"""The PostgreSQL adapters of the credentials and sessions modules.

The test needs a PostgreSQL server: set MAI_TEST_DSN, for example
postgresql://postgres:pw@127.0.0.1:55432/postgres.
"""

import datetime as dt
import os

import anyio
import pytest
from bustan import ConfigService

DSN = os.environ.get("MAI_TEST_DSN")
pytestmark = pytest.mark.skipif(not DSN, reason="MAI_TEST_DSN is not set")


class _Config(ConfigService):
    pass


def test_postgres_round_trip():
    from mai.modules.credentials import MIGRATIONS as CREDENTIAL_MIGRATIONS
    from mai.modules.credentials.domain.credential import Principal
    from mai.modules.credentials.infra.postgres_credential_store import (
        PostgresCredentialStore,
    )
    from mai.modules.sessions import MIGRATIONS as SESSION_MIGRATIONS
    from mai.modules.sessions.infra.postgres_handle_store import (
        PostgresHandleStore,
    )
    from mai.platform.database import Database

    database = Database(_Config({"DATABASE_BACKEND": "postgres", "DATABASE_DSN": DSN}))

    async def go():
        await database.migrate([*CREDENTIAL_MIGRATIONS, *SESSION_MIGRATIONS])
        store = PostgresCredentialStore(database)
        handles = PostgresHandleStore(database)
        now = dt.datetime.now(dt.UTC)
        await store.add(Principal(login="cursor", kind="agent", status="active"))
        added = await store.add(
            Principal(
                login="cursor.pg",
                kind="session",
                status="issued",
                agent="cursor",
                label="pg",
                password_hash="h1",
                expires_at=now + dt.timedelta(days=1),
            )
        )
        again = await store.add(
            Principal(login="cursor", kind="agent", status="active")
        )
        await store.add_alias("user", "cursor")
        await handles.add("sha1", "cursor.pg", now + dt.timedelta(hours=1))
        live = await handles.extend("sha1", now + dt.timedelta(hours=2), now)
        await store.set_hash("cursor.pg", "h2", now)
        after_rotate = await handles.extend(
            "sha1", now + dt.timedelta(hours=2), dt.datetime.now(dt.UTC)
        )
        await store.mark_login("cursor.pg", now)
        p = await store.get("cursor.pg")
        sessions = await store.sessions("cursor")
        await store.set_status("cursor.pg", "revoked", now)
        revoked = await store.get("cursor.pg")
        roles = await store.roles("cursor")
        return added, again, live, after_rotate, p, sessions, revoked, roles

    added, again, live, after_rotate, p, sessions, revoked, roles = anyio.run(go)
    assert added and not again
    assert live == "cursor.pg" and after_rotate is None
    assert p.status == "active" and p.password_hash == "h2"
    assert [s.login for s in sessions] == ["cursor.pg"]
    assert revoked.status == "revoked"
    assert roles == {}
