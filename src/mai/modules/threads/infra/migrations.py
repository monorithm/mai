"""The migrations of the threads module (versions 300 to 399).

Version 300 makes the table of thread claims, with the column that records
the stale signal of each lease, and grants it to the mai role.
"""

from mai.platform.database import Migration

_V300 = """
CREATE TABLE IF NOT EXISTS mail_auth.thread_claim (
  agent        text NOT NULL REFERENCES mail_auth.principal(login),
  root_msg_id  text NOT NULL,
  login        text NOT NULL REFERENCES mail_auth.principal(login),
  lease_end    timestamptz NOT NULL,
  PRIMARY KEY (agent, root_msg_id)
);

ALTER TABLE mail_auth.thread_claim ADD COLUMN IF NOT EXISTS stale_signalled_for timestamptz;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'mai')
     AND current_user <> 'mai' THEN
    GRANT SELECT, INSERT, UPDATE ON mail_auth.thread_claim TO mai;
  END IF;
END
$$;
"""

MIGRATIONS = [Migration(300, "thread_claim", _V300)]
