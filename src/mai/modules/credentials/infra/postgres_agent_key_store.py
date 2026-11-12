from datetime import datetime

from mai.platform.database import Database, Migration

from ..domain.agent_key_store import AgentKeyStore

# The daemons do not read this table. Only the server reads it.
_V210 = """
CREATE TABLE IF NOT EXISTS mail_auth.agent_key (
  agent       text PRIMARY KEY REFERENCES mail_auth.principal(login),
  key_hash    text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'mai')
     AND current_user <> 'mai' THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON mail_auth.agent_key TO mai;
  END IF;
END
$$;
"""

MIGRATIONS = [Migration(210, "create the table of the agent key hashes", _V210)]


class PostgresAgentKeyStore(AgentKeyStore):
    """Keeps the hashes of the agent keys in the table mail_auth.agent_key."""

    def __init__(self, database: Database) -> None:
        self._database = database

    async def get_hash(self, agent: str) -> str | None:
        async with self._database.connection() as conn:
            cur = await conn.execute(
                "SELECT key_hash FROM mail_auth.agent_key WHERE agent = %s", (agent,)
            )
            row = await cur.fetchone()
        return None if row is None else str(row[0])

    async def set_hash(self, agent: str, key_hash: str, now: datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "INSERT INTO mail_auth.agent_key (agent, key_hash, updated_at)"
                " VALUES (%s, %s, %s) ON CONFLICT (agent)"
                " DO UPDATE SET key_hash = EXCLUDED.key_hash,"
                " updated_at = EXCLUDED.updated_at",
                (agent, key_hash, now),
            )

    async def remove(self, agent: str) -> bool:
        async with self._database.transaction() as conn:
            cur = await conn.execute(
                "DELETE FROM mail_auth.agent_key WHERE agent = %s", (agent,)
            )
            return bool(cur.rowcount)
