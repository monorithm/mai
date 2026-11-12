from __future__ import annotations

import datetime as dt
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

from ..domain.claim import Claim, ClaimStore, ClaimTransaction

_COLUMNS = "agent, root_msg_id, login, lease_end"


def _claim(row) -> Claim:
    return Claim(row[0], row[1], row[2], row[3])


def _claim_or_none(row) -> Claim | None:
    return _claim(row) if row else None


# A session that can log in: the same rule as the passdb of Dovecot.
# clock_timestamp(): the check runs inside a claim lock, after the lock wait.
_SESSION_LIVE = (
    "p.status IN ('issued', 'active')"
    " AND (p.expires_at IS NULL OR p.expires_at > clock_timestamp())"
)


async def _one(conn, sql: str, params=()):
    cur = await conn.execute(sql, params)
    return await cur.fetchone()


async def _all(conn, sql: str, params=()):
    cur = await conn.execute(sql, params)
    return await cur.fetchall()


class _PostgresTransaction(ClaimTransaction):
    def __init__(self, conn, agent: str, root: str) -> None:
        self._conn = conn
        self._agent = agent
        self._root = root

    async def get(self) -> Claim | None:
        return _claim_or_none(
            await _one(
                self._conn,
                f"SELECT {_COLUMNS} FROM mail_auth.thread_claim WHERE agent = %s AND root_msg_id = %s",
                (self._agent, self._root),
            )
        )

    async def put(self, login: str, lease: dt.timedelta) -> Claim:
        row = await _one(
            self._conn,
            "INSERT INTO mail_auth.thread_claim (agent, root_msg_id, login, lease_end)"
            " VALUES (%s, %s, %s, clock_timestamp() + %s)"
            " ON CONFLICT (agent, root_msg_id) DO UPDATE"
            " SET login = EXCLUDED.login, lease_end = EXCLUDED.lease_end"
            f" RETURNING {_COLUMNS}",
            (self._agent, self._root, login, lease),
        )
        # INSERT ... ON CONFLICT DO UPDATE ... RETURNING always gives one row.
        return _claim(row)

    async def end(self) -> None:
        await self._conn.execute(
            "UPDATE mail_auth.thread_claim SET lease_end = clock_timestamp()"
            " WHERE agent = %s AND root_msg_id = %s",
            (self._agent, self._root),
        )

    async def now(self) -> dt.datetime:
        # now() is the start of the transaction, which is before the lock wait.
        return (await _one(self._conn, "SELECT clock_timestamp()"))[0]


class PostgresClaimStore(ClaimStore):
    """Claims in mail_auth.thread_claim. An advisory transaction lock on (agent, root) serializes the claims."""

    def __init__(self, database) -> None:
        self._database = database

    @asynccontextmanager
    async def locked(self, agent: str, root: str) -> AsyncGenerator[ClaimTransaction]:
        async with self._database.transaction() as conn:
            await conn.execute(
                "SELECT pg_advisory_xact_lock(hashtext(%s), hashtext(%s))",
                (agent, root),
            )
            yield _PostgresTransaction(conn, agent, root)

    async def roots_of(self, agent: str) -> list[str]:
        async with self._database.connection() as conn:
            rows = await _all(
                conn,
                "SELECT root_msg_id FROM mail_auth.thread_claim WHERE agent = %s",
                (agent,),
            )
        return [r[0] for r in rows]

    async def live_holder(self, agent: str, root: str) -> str | None:
        async with self._database.connection() as conn:
            row = await _one(
                conn,
                "SELECT c.login FROM mail_auth.thread_claim c"
                " JOIN mail_auth.principal p ON p.login = c.login"
                " WHERE c.agent = %s AND c.root_msg_id = %s AND c.lease_end > now()"
                f" AND {_SESSION_LIVE}",
                (agent, root),
            )
        return row[0] if row else None

    async def session_live(self, login: str) -> bool:
        async with self._database.connection() as conn:
            row = await _one(
                conn,
                f"SELECT 1 FROM mail_auth.principal p WHERE p.login = %s AND {_SESSION_LIVE}",
                (login,),
            )
        return row is not None

    async def ended_unsignalled(self) -> list[Claim]:
        async with self._database.connection() as conn:
            rows = await _all(
                conn,
                f"SELECT {_COLUMNS} FROM mail_auth.thread_claim WHERE lease_end <= now()"
                " AND (stale_signalled_for IS NULL OR stale_signalled_for <> lease_end)",
            )
        return [_claim(r) for r in rows]

    async def mark_signalled(self, claim: Claim) -> bool:
        async with self._database.transaction() as conn:
            row = await _one(
                conn,
                "UPDATE mail_auth.thread_claim SET stale_signalled_for = lease_end"
                " WHERE agent = %s AND root_msg_id = %s AND lease_end = %s"
                " AND (stale_signalled_for IS NULL OR stale_signalled_for <> lease_end)"
                " RETURNING agent",
                (claim.agent, claim.root_msg_id, claim.lease_end),
            )
        return row is not None
