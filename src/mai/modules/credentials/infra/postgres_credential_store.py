from datetime import datetime

from mai.platform.database import Database, Migration

from ..domain.credential import Principal
from ..domain.credential_store import CredentialStore

# Dovecot and Postfix read these tables and views, so their names stay fixed.
# Add a migration with the next version for each change. Never edit one.
_V100 = """
CREATE SCHEMA IF NOT EXISTS mail_auth;

CREATE TABLE IF NOT EXISTS mail_auth.principal (
  login          text PRIMARY KEY,
  kind           text NOT NULL CHECK (kind IN ('agent','session','owner','system')),
  agent          text REFERENCES mail_auth.principal(login),
  label          text,
  password_hash  text,
  status         text NOT NULL CHECK (status IN ('issued','active','expired','revoked')),
  expires_at     timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  last_login_at  timestamptz
);

-- Extra mail addresses that deliver to a principal, for example "user" to "owner".
CREATE TABLE IF NOT EXISTS mail_auth.alias (
  address  text PRIMARY KEY,
  login    text NOT NULL REFERENCES mail_auth.principal(login)
);

CREATE TABLE IF NOT EXISTS mail_auth.membership (
  project  text NOT NULL,
  agent    text NOT NULL REFERENCES mail_auth.principal(login),
  role     text NOT NULL,
  PRIMARY KEY (project, agent)
);

-- Postfix checks that the SASL login owns the envelope sender.
-- A login owns its own address only.
CREATE OR REPLACE VIEW mail_auth.sender_login AS
SELECT login || '@mai.internal' AS address, login
  FROM mail_auth.principal
 WHERE kind IN ('session','owner','system')
   AND status IN ('issued','active');

-- Mail addresses that Dovecot and Postfix accept for delivery.
CREATE OR REPLACE VIEW mail_auth.mailbox_address AS
SELECT login || '@mai.internal' AS address, login
  FROM mail_auth.principal
 WHERE status IN ('issued','active') OR kind = 'agent'
UNION ALL
SELECT address || '@mai.internal', login FROM mail_auth.alias;

-- Grants for the daemon roles, when the roles exist and the migration
-- role can grant. The container controller makes the roles.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dovecot') THEN
    GRANT USAGE ON SCHEMA mail_auth TO dovecot;
    GRANT SELECT ON mail_auth.principal TO dovecot;
    GRANT SELECT ON mail_auth.alias TO dovecot;
    GRANT SELECT ON mail_auth.mailbox_address TO dovecot;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'postfix') THEN
    GRANT USAGE ON SCHEMA mail_auth TO postfix;
    GRANT SELECT ON mail_auth.sender_login TO postfix;
    GRANT SELECT ON mail_auth.mailbox_address TO postfix;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'mai')
     AND current_user <> 'mai' THEN
    GRANT USAGE ON SCHEMA mail_auth TO mai;
    GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA mail_auth TO mai;
  END IF;
END
$$;
"""
MIGRATIONS = [
    Migration(100, "create the accounts, aliases, roles, and the mail views", _V100)
]

_COLUMNS = "login, kind, status, agent, label, password_hash, expires_at, created_at, last_login_at"

# The handle table belongs to the sessions module and the claim table to the
# threads module. A change of the secret or a revoke ends their rows when the
# tables exist, in the same transaction as the change of the principal.


class PostgresCredentialStore(CredentialStore):
    """Keeps the accounts in the schema mail_auth of PostgreSQL."""

    def __init__(self, database: Database) -> None:
        self._database = database

    async def get(self, login: str) -> Principal | None:
        async with self._database.connection() as conn:
            cur = await conn.execute(
                f"SELECT {_COLUMNS} FROM mail_auth.principal WHERE login = %s", (login,)
            )
            row = await cur.fetchone()
        return None if row is None else _principal(row)

    async def add(self, principal: Principal) -> bool:
        p = principal
        async with self._database.transaction() as conn:
            cur = await conn.execute(
                "INSERT INTO mail_auth.principal"
                " (login, kind, status, agent, label, password_hash, expires_at)"
                " VALUES (%s, %s, %s, %s, %s, %s, %s) ON CONFLICT (login) DO NOTHING",
                (
                    p.login,
                    p.kind,
                    p.status,
                    p.agent,
                    p.label,
                    p.password_hash,
                    p.expires_at,
                ),
            )
            return bool(cur.rowcount)

    async def add_alias(self, alias: str, login: str) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "INSERT INTO mail_auth.alias (address, login) VALUES (%s, %s)"
                " ON CONFLICT (address) DO NOTHING",
                (alias, login),
            )

    async def set_hash(self, login: str, password_hash: str, now: datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "UPDATE mail_auth.principal SET password_hash = %s WHERE login = %s",
                (password_hash, login),
            )
            await _end_handles(conn, login)

    async def set_status(self, login: str, status: str, now: datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "UPDATE mail_auth.principal SET status = %s WHERE login = %s",
                (status, login),
            )
            if status == "revoked":
                await _end_handles(conn, login)
                cur = await conn.execute(
                    "SELECT to_regclass('mail_auth.thread_claim') IS NOT NULL"
                )
                row = await cur.fetchone()
                if row and row[0]:
                    # The same lock as each claim, release, and route of the thread: a route
                    # that holds the lock ends its move before the claim ends.
                    await conn.execute(
                        "SELECT pg_advisory_xact_lock(hashtext(agent), hashtext(root_msg_id))"
                        " FROM mail_auth.thread_claim WHERE login = %s AND lease_end > clock_timestamp()",
                        (login,),
                    )
                    await conn.execute(
                        "UPDATE mail_auth.thread_claim SET lease_end = clock_timestamp()"
                        " WHERE login = %s AND lease_end > clock_timestamp()",
                        (login,),
                    )

    async def mark_login(self, login: str, now: datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "UPDATE mail_auth.principal SET status = 'active', last_login_at = %s"
                " WHERE login = %s",
                (now, login),
            )

    async def expire_old(self, now: datetime) -> None:
        async with self._database.transaction() as conn:
            await conn.execute(
                "UPDATE mail_auth.principal SET status = 'expired'"
                " WHERE kind = 'session' AND status IN ('issued','active') AND expires_at <= %s",
                (now,),
            )

    async def agents(self) -> list[Principal]:
        async with self._database.connection() as conn:
            cur = await conn.execute(
                f"SELECT {_COLUMNS} FROM mail_auth.principal WHERE kind = 'agent' ORDER BY login"
            )
            rows = await cur.fetchall()
        return [_principal(r) for r in rows]

    async def sessions(self, agent: str | None) -> list[Principal]:
        sql = f"SELECT {_COLUMNS} FROM mail_auth.principal WHERE kind = 'session'"
        params: tuple = ()
        if agent:
            sql += " AND agent = %s"
            params = (agent,)
        async with self._database.connection() as conn:
            cur = await conn.execute(sql + " ORDER BY login", params)
            rows = await cur.fetchall()
        return [_principal(r) for r in rows]

    async def roles(self, agent: str) -> dict[str, str]:
        async with self._database.connection() as conn:
            cur = await conn.execute(
                "SELECT project, role FROM mail_auth.membership WHERE agent = %s",
                (agent,),
            )
            rows = await cur.fetchall()
        return {r[0]: r[1] for r in rows}


async def _end_handles(conn, login: str) -> None:
    cur = await conn.execute(
        "SELECT to_regclass('mail_auth.session_handle') IS NOT NULL"
    )
    row = await cur.fetchone()
    if row and row[0]:
        await conn.execute(
            "UPDATE mail_auth.session_handle SET expires_at = now()"
            " WHERE login = %s AND expires_at > now()",
            (login,),
        )


def _principal(row) -> Principal:
    return Principal(
        login=row[0],
        kind=row[1],
        status=row[2],
        agent=row[3],
        label=row[4],
        password_hash=row[5],
        expires_at=row[6],
        created_at=row[7],
        last_login_at=row[8],
    )
