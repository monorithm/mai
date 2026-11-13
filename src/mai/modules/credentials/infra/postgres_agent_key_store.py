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

# The app role has no DELETE right, thus the removal of a key keeps the row and
# sets the hash to NULL. The version follows 210, which makes the table.
_V211 = """
ALTER TABLE mail_auth.agent_key ALTER COLUMN key_hash DROP NOT NULL;
"""

MIGRATIONS = [
    Migration(210, "create the table of the agent key hashes", _V210),
    Migration(211, "keep the row of a removed agent key with no hash", _V211),
]


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
        # A removed key keeps its row with no hash.
        return None if row is None or row[0] is None else str(row[0])

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
        # An UPDATE, not a DELETE: the app role has no DELETE right.
        async with self._database.transaction() as conn:
            cur = await conn.execute(
                "UPDATE mail_auth.agent_key SET key_hash = NULL, updated_at = now()"
                " WHERE agent = %s AND key_hash IS NOT NULL",
                (agent,),
            )
            return bool(cur.rowcount)
