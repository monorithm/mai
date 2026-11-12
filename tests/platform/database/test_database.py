import asyncio
import os

import pytest
from bustan import ConfigService

from mai.platform.database import (
    Database,
    DatabaseBackend,
    Migration,
    migrate_sync,
)
from mai.platform.database.database import _check


def _db(**values: str) -> Database:
    return Database(ConfigService({"DATABASE_BACKEND": "memory", **values}))


def test_memory_backend_opens_nothing():
    db = _db()
    assert db.backend is DatabaseBackend.MEMORY and not db.is_postgres
    assert asyncio.run(db.migrate([Migration(100, "x", "SELECT 1")])) == []
    with pytest.raises(RuntimeError):
        asyncio.run(db.fetch_one("SELECT 1"))


def test_migrations_sort_by_version_and_refuse_duplicates():
    a, b = Migration(500, "b", "SELECT 1"), Migration(100, "a", "SELECT 1")
    assert [m.version for m in _check([a, b])] == [100, 500]
    with pytest.raises(ValueError):
        _check([a, Migration(500, "c", "SELECT 1")])


@pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="needs PostgreSQL")
def test_migrate_sync_applies_each_version_once():
    dsn = os.environ.get("DATABASE_DSN", "host=/run/postgresql dbname=mai user=mai")
    items = [
        Migration(699, "test", "CREATE TABLE IF NOT EXISTS mail_auth.it_probe (x int)")
    ]
    migrate_sync(items, dsn)
    assert migrate_sync(items, dsn) == []
