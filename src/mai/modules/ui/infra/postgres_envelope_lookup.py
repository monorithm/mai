"""Reads the envelope records of mail_auth.envelope, if that table exists."""

from __future__ import annotations

from mai.platform.database import Database

from ..domain.mail_view import EnvelopeLookup, ThreadClaim, ThreadLookup


class PostgresEnvelopeLookup(EnvelopeLookup):
    def __init__(self, database: Database) -> None:
        self._database = database

    async def find(self, message_ids: list[str]) -> dict[str, dict]:
        if not message_ids or not self._database.is_postgres:
            return {}
        try:
            async with self._database.connection() as conn:
                cur = await conn.execute(
                    "SELECT to_regclass('mail_auth.envelope') IS NOT NULL"
                )
                row = await cur.fetchone()
                if not row or not row[0]:
                    return {}
                cur = await conn.execute(
                    "SELECT e.message_id, to_jsonb(e) FROM mail_auth.envelope e "
                    "WHERE e.message_id = ANY(%s)",
                    (message_ids,),
                )
                rows = await cur.fetchall()
        except Exception:  # noqa: BLE001 - a missing column gives no envelope, not an error page
            return {}
        return {str(r[0]): dict(r[1]) for r in rows if isinstance(r[1], dict)}


class NoEnvelopeLookup(EnvelopeLookup):
    async def find(self, message_ids: list[str]) -> dict[str, dict]:
        return {}


class PostgresThreadLookup(ThreadLookup):
    """Reads the claims of mail_auth.thread_claim, if that table exists."""

    def __init__(self, database: Database) -> None:
        self._database = database

    async def claims(self) -> list[ThreadClaim]:
        if not self._database.is_postgres:
            return []
        try:
            async with self._database.connection() as conn:
                cur = await conn.execute(
                    "SELECT to_regclass('mail_auth.thread_claim') IS NOT NULL"
                )
                row = await cur.fetchone()
                if not row or not row[0]:
                    return []
                cur = await conn.execute(
                    "SELECT agent, root_msg_id, login, lease_end, lease_end > now() "
                    "FROM mail_auth.thread_claim ORDER BY lease_end DESC LIMIT 500"
                )
                rows = await cur.fetchall()
        except Exception:  # noqa: BLE001 - a read error gives an empty view
            return []
        return [
            ThreadClaim(str(r[0]), str(r[1]), str(r[2]), str(r[3]), bool(r[4]))
            for r in rows
        ]
