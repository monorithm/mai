"""The PostgreSQL adapters of the mail module: the retry keys and the directory of principals."""

from __future__ import annotations

from bustan import Injectable

from mai.platform.database import Database, Migration

from ..domain.errors import MailError
from ..domain.ports import Directory, SentRequests, SessionEntry

# The table can already exist from the first schema of mai, thus each
# statement is safe to run again.
_V400 = """
CREATE SCHEMA IF NOT EXISTS mail_auth;
CREATE TABLE IF NOT EXISTS mail_auth.sent_request (
  login        text NOT NULL REFERENCES mail_auth.principal(login),
  request_id   text NOT NULL,
  content_sha  text NOT NULL,
  message_id   text NOT NULL,
  PRIMARY KEY (login, request_id)
);
"""

# The milter writes the envelope of each accepted mail: the copy in the
# mailbox "copies" holds the headers only, thus it does not show a Bcc
# recipient. The milter connects as the role postfix, which gets INSERT only.
# The app role mai reads the rows for the owner.
_V410 = """
CREATE TABLE IF NOT EXISTS mail_auth.envelope (
  queue_id    text,
  message_id  text,
  sender      text NOT NULL,
  recipients  text[] NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS envelope_message_id_idx ON mail_auth.envelope (message_id);
CREATE INDEX IF NOT EXISTS envelope_created_at_idx ON mail_auth.envelope (created_at);
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'postfix') THEN
    GRANT USAGE ON SCHEMA mail_auth TO postfix;
    GRANT INSERT ON mail_auth.envelope TO postfix;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'mai')
     AND current_user <> 'mai' THEN
    GRANT SELECT, DELETE ON mail_auth.envelope TO mai;
  END IF;
END
$$;
"""

MIGRATIONS = [
    Migration(400, "create the table mail_auth.sent_request", _V400),
    Migration(410, "create the table mail_auth.envelope", _V410),
]

_LIVE = "(kind <> 'session' OR (status IN ('issued','active') AND (expires_at IS NULL OR expires_at > now())))"


@Injectable()
class PostgresSentRequests(SentRequests):
    def __init__(self, database: Database) -> None:
        self._database = database

    async def reserve(self, login, request_id, content_sha, message_id):
        async with self._database.transaction() as conn:
            cur = await conn.execute(
                "SELECT content_sha, message_id FROM mail_auth.sent_request "
                "WHERE login = %s AND request_id = %s FOR UPDATE",
                (login, request_id),
            )
            row = await cur.fetchone()
            if row is not None:
                if row[0] != content_sha:
                    raise MailError("RETRY_KEY_REUSED", path="request_id")
                return row[1], True
            cur = await conn.execute(
                "INSERT INTO mail_auth.sent_request (login, request_id, content_sha, message_id) "
                "VALUES (%s, %s, %s, %s) ON CONFLICT DO NOTHING RETURNING message_id",
                (login, request_id, content_sha, message_id),
            )
            if await cur.fetchone() is None:
                # A parallel call with the same key won the insert.
                raise MailError("RETRY_KEY_REUSED", path="request_id")
            return message_id, False

    async def release(self, login, request_id, message_id):
        async with self._database.transaction() as conn:
            await conn.execute(
                "DELETE FROM mail_auth.sent_request WHERE login = %s AND request_id = %s AND message_id = %s",
                (login, request_id, message_id),
            )


@Injectable()
class PostgresDirectory(Directory):
    def __init__(self, database: Database) -> None:
        self._database = database

    async def known_logins(self, logins):
        async with self._database.connection() as conn:
            cur = await conn.execute(
                f"SELECT login FROM mail_auth.principal WHERE login = ANY(%s) AND {_LIVE}",
                (list(logins),),
            )
            return {row[0] for row in await cur.fetchall()}

    async def agents(self):
        async with self._database.connection() as conn:
            cur = await conn.execute(
                "SELECT login FROM mail_auth.principal WHERE kind = 'agent' ORDER BY login"
            )
            result: dict[str, list[SessionEntry]] = {
                row[0]: [] for row in await cur.fetchall()
            }
            cur = await conn.execute(
                "SELECT agent, login, label, last_login_at FROM mail_auth.principal "
                f"WHERE kind = 'session' AND {_LIVE} ORDER BY login"
            )
            for agent, login, label, last in await cur.fetchall():
                result.setdefault(agent, []).append(SessionEntry(login, label, last))
        return dict(sorted(result.items()))
