import datetime as dt

from mai.platform.database import Database, Migration

from ..domain.handle_store import HandleStore

# Add a migration with the next version for each change. Never edit one.
_V200 = """
CREATE TABLE IF NOT EXISTS mail_auth.session_handle (
  handle_sha  text PRIMARY KEY,
  login       text NOT NULL REFERENCES mail_auth.principal(login),
  expires_at  timestamptz NOT NULL
);

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'mai')
     AND current_user <> 'mai' THEN
    GRANT SELECT, INSERT, UPDATE ON mail_auth.session_handle TO mai;
  END IF;
END
$$;
"""
MIGRATIONS = [Migration(200, "create the table of session handles", _V200)]


class PostgresHandleStore(HandleStore):
    """Keeps the handles in mail_auth.session_handle.

    extend() also checks the principal: a revoked or expired login gives None.
    """

    def __init__(self, database: Database) -> None:
        self._database = database

    async def add(self, handle_sha: str, login: str, expires_at: dt.datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "INSERT INTO mail_auth.session_handle (handle_sha, login, expires_at)"
                " VALUES (%s, %s, %s)",
                (handle_sha, login, expires_at),
            )

    async def extend(
        self, handle_sha: str, expires_at: dt.datetime, now: dt.datetime
    ) -> str | None:
        async with self._database.transaction() as conn:
            cur = await conn.execute(
                "UPDATE mail_auth.session_handle h SET expires_at = %s"
                " FROM mail_auth.principal p"
                " WHERE h.handle_sha = %s AND p.login = h.login AND h.expires_at > %s"
                " AND p.status IN ('issued','active')"
                " AND (p.expires_at IS NULL OR p.expires_at > %s)"
                " RETURNING h.login",
                (expires_at, handle_sha, now, now),
            )
            row = await cur.fetchone()
        return None if row is None else row[0]

    async def end(self, handle_sha: str, now: dt.datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "UPDATE mail_auth.session_handle SET expires_at = %s WHERE handle_sha = %s",
                (now, handle_sha),
            )
