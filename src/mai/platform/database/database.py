"""The PostgreSQL database of mai.

The app connects as the role mai over the Unix socket. Dovecot and
Postfix read the tables of the schema mail_auth, thus production always uses
PostgreSQL. The memory backend opens nothing: each storage port then uses its
memory adapter.
"""

from __future__ import annotations

from collections.abc import AsyncGenerator, Iterable, Sequence
from contextlib import asynccontextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from enum import StrEnum
from typing import TYPE_CHECKING, LiteralString

from bustan import ConfigService, Injectable

if TYPE_CHECKING:
    import psycopg

# psycopg needs libpq, which only the container has. Each function imports it
# when it runs, thus the memory backend works without libpq.
DEFAULT_DSN = "host=/run/postgresql dbname=mai user=mai"


class DatabaseBackend(StrEnum):
    MEMORY = "memory"
    POSTGRES = "postgres"


@dataclass(frozen=True, slots=True)
class Migration:
    """One change to the schema. Never change a migration after a release.

    Each module has its own range of versions. To change a table, add a
    migration with the next version of the range.
    """

    version: int
    name: str
    sql: LiteralString


# The version table. The old table schema_migration kept file names; a
# database made by the flat layout has those names, and they map to the
# first version of their module.
_VERSION_TABLE = """
CREATE SCHEMA IF NOT EXISTS mail_auth;
CREATE TABLE IF NOT EXISTS mail_auth.schema_version (
  version     integer PRIMARY KEY,
  name        text NOT NULL,
  applied_at  timestamptz NOT NULL DEFAULT now()
);
"""
_LEGACY_FILES = {"0001_mail_auth.sql": 100, "0002_board.sql": 500}


def _check(migrations: Sequence[Migration]) -> list[Migration]:
    ordered = sorted(migrations, key=lambda m: m.version)
    versions = [m.version for m in ordered]
    if len(set(versions)) != len(versions):
        raise ValueError("two migrations have the same version")
    return ordered


def _legacy_versions(conn: psycopg.Connection) -> set[int]:
    row = conn.execute("SELECT to_regclass('mail_auth.schema_migration')").fetchone()
    if not row or row[0] is None:
        return set()
    names = {r[0] for r in conn.execute("SELECT name FROM mail_auth.schema_migration")}
    return {v for n, v in _LEGACY_FILES.items() if n in names}


def _revoke_delete(conn: psycopg.Connection) -> None:
    # The app role never erases a row. Remove DELETE and TRUNCATE from each
    # table of the app schemas, also when the app role owns the table.
    if not conn.execute("SELECT 1 FROM pg_roles WHERE rolname = 'mai'").fetchone():
        return
    from psycopg import sql

    rows = conn.execute(
        """
        SELECT n.nspname, c.relname
          FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE c.relkind IN ('r', 'p')
           AND n.nspname NOT LIKE 'pg\\_%' AND n.nspname <> 'information_schema'
           AND n.nspname <> 'public'
        """
    ).fetchall()
    for schema, table in rows:
        if table in ("schema_version", "schema_migration"):
            continue
        conn.execute(
            sql.SQL("REVOKE DELETE, TRUNCATE ON {} FROM mai").format(
                sql.Identifier(schema, table)
            )
        )


def migrate_sync(migrations: Iterable[Migration], dsn: str | None = None) -> list[int]:
    """Apply each migration once, in version order. Return the applied versions.

    The container controller calls this before the daemons start.
    """
    import psycopg

    ordered = _check(list(migrations))
    applied: list[int] = []
    with psycopg.connect(dsn or DEFAULT_DSN, autocommit=False) as conn:
        conn.execute(_VERSION_TABLE)
        legacy = _legacy_versions(conn)
        for version in legacy:
            conn.execute(
                "INSERT INTO mail_auth.schema_version (version, name) VALUES (%s, %s)"
                " ON CONFLICT DO NOTHING",
                (version, "legacy"),
            )
        conn.commit()
        done = {
            r[0] for r in conn.execute("SELECT version FROM mail_auth.schema_version")
        }
        for m in ordered:
            if m.version in done:
                continue
            conn.execute(m.sql)
            conn.execute(
                "INSERT INTO mail_auth.schema_version (version, name) VALUES (%s, %s)",
                (m.version, m.name),
            )
            conn.commit()
            applied.append(m.version)
        _revoke_delete(conn)
        conn.commit()
    return applied


def _psycopg():
    import psycopg

    return psycopg


_CURRENT: ContextVar[psycopg.AsyncConnection | None] = ContextVar(
    "mai_database_transaction", default=None
)


@Injectable()
class Database:
    """The PostgreSQL access of the process, if DATABASE_BACKEND is postgres.

    connection() gives a connection in autocommit mode, or the connection of
    the open transaction() of the same task. transaction() commits at the
    end of the block, or rolls back on an error.
    """

    def __init__(self, config: ConfigService) -> None:
        self.backend = DatabaseBackend(str(config.get("DATABASE_BACKEND", "memory")))
        self.dsn = str(config.get("DATABASE_DSN", DEFAULT_DSN) or DEFAULT_DSN)

    @property
    def is_postgres(self) -> bool:
        return self.backend is DatabaseBackend.POSTGRES

    @property
    def in_transaction(self) -> bool:
        return _CURRENT.get() is not None

    def _require(self) -> None:
        if not self.is_postgres:
            raise RuntimeError("DATABASE_BACKEND is memory: no PostgreSQL connection")

    @asynccontextmanager
    async def connection(self) -> AsyncGenerator[psycopg.AsyncConnection]:
        self._require()
        current = _CURRENT.get()
        if current is not None:
            yield current
            return
        conn = await _psycopg().AsyncConnection.connect(self.dsn, autocommit=True)
        try:
            yield conn
        finally:
            await conn.close()

    @asynccontextmanager
    async def transaction(self) -> AsyncGenerator[psycopg.AsyncConnection]:
        self._require()
        current = _CURRENT.get()
        if current is not None:
            # A nested block joins the open transaction as a savepoint.
            async with current.transaction():
                yield current
            return
        conn = await _psycopg().AsyncConnection.connect(self.dsn, autocommit=True)
        token = _CURRENT.set(conn)
        try:
            async with conn.transaction():
                yield conn
        finally:
            _CURRENT.reset(token)
            await conn.close()

    async def fetch_one(
        self, sql: LiteralString, params: Sequence[object] = ()
    ) -> tuple | None:
        async with self.connection() as conn:
            cur = await conn.execute(sql, params)
            return await cur.fetchone()

    async def migrate(self, migrations: Iterable[Migration]) -> list[int]:
        """Apply the migrations (async). Does nothing on the memory backend."""
        if not self.is_postgres:
            return []
        import anyio.to_thread

        items = list(migrations)
        return await anyio.to_thread.run_sync(lambda: migrate_sync(items, self.dsn))
