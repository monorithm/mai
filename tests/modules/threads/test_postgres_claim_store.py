"""The PostgreSQL adapter of ClaimStore. It runs only when MAI_PG_DSN names a test database."""

import datetime as dt
import os

import pytest
from bustan import ConfigService

from mai.modules.threads import MIGRATIONS
from mai.modules.threads.infra.postgres_claim_store import PostgresClaimStore
from mai.platform.database import Database

DSN = os.environ.get("MAI_PG_DSN")
pytestmark = [
    pytest.mark.anyio,
    pytest.mark.skipif(not DSN, reason="MAI_PG_DSN is not set"),
]

SCHEMA = """
DROP SCHEMA IF EXISTS mail_auth CASCADE;
CREATE SCHEMA mail_auth;
CREATE TABLE mail_auth.thread_claim (
  agent text NOT NULL, root_msg_id text NOT NULL, login text NOT NULL,
  lease_end timestamptz NOT NULL, PRIMARY KEY (agent, root_msg_id));
"""


@pytest.fixture
def anyio_backend():
    return "asyncio"


class Config(ConfigService):
    def __init__(self):
        super().__init__({})

    def get(self, key: str, default=None):
        return {"DATABASE_BACKEND": "postgres", "DATABASE_DSN": DSN}.get(key, default)


async def test_claim_cycle_and_stale_signal_once():
    database = Database(Config())
    async with database.connection() as conn:
        await conn.execute(SCHEMA)
        for m in MIGRATIONS:
            await conn.execute(m.sql)
    store = PostgresClaimStore(database)
    async with store.locked("cursor", "<r@x>") as txn:
        assert await txn.get() is None
        claim = await txn.put("cursor.lane-a", dt.timedelta(minutes=30))
    assert claim.login == "cursor.lane-a"
    assert await store.live_holder("cursor", "<r@x>") == "cursor.lane-a"
    assert await store.roots_of("cursor") == ["<r@x>"]
    with pytest.raises(RuntimeError):
        async with store.locked("cursor", "<r@x>") as txn:
            await txn.end()
            raise RuntimeError("roll back")
    assert await store.live_holder("cursor", "<r@x>") == "cursor.lane-a"
    async with store.locked("cursor", "<r@x>") as txn:
        await txn.end()
    assert await store.live_holder("cursor", "<r@x>") is None
    [ended] = await store.ended_unsignalled()
    assert await store.mark_signalled(ended) is True
    assert await store.mark_signalled(ended) is False
    assert await store.ended_unsignalled() == []
