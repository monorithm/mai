"""The retry keys of send_message, as the app role mai, in the container.

The container test pipes this script into the Python of the image. The
script connects with the DSN of the app (role mai over the Unix socket). It
prints one RESULT line for each step and no secret.
"""

import datetime as dt
import uuid

import anyio
from bustan import ConfigService

from mai.modules.mail.infra.postgres_mail_store import PostgresSentRequests
from mai.platform.database import Database

NAMES: dict[str, str] = {}


class Config(ConfigService):
    def __init__(self):
        super().__init__({})

    def get(self, key, default=None):
        return {"DATABASE_BACKEND": "postgres"}.get(key, default)


def login_of(path):
    with open(path) as f:
        return f.read().strip().split(":")[1]


def message_id(name):
    value = f"<it-{uuid.uuid4()}@mai.internal>"
    NAMES[value] = name
    return value


def show(name, kept):
    print(
        f"RESULT {name} duplicate={kept.duplicate}"
        f" kept={NAMES.get(kept.message_id, 'other')} dated={kept.sent_at is not None}"
    )


async def main():
    database = Database(Config())
    sent = PostgresSentRequests(database)
    login = login_of("/data/lanea.cred")

    # A send that fails frees its key. The app role has no DELETE right.
    key = "it-" + uuid.uuid4().hex
    m1 = message_id("m1")
    await sent.reserve(login, key, "sha", m1)
    try:
        await sent.release(login, key, m1)
        print("RESULT release done")
    except Exception as exc:  # noqa: BLE001 - the RESULT line gives the type
        print(f"RESULT release {type(exc).__name__}")
    m2 = message_id("m2")
    show("retry", await sent.reserve(login, key, "sha", m2))
    # A sent mail gives a replay with its date.
    await sent.mark_sent(login, key, m2, dt.datetime.now(dt.UTC))
    show("replay", await sent.reserve(login, key, "sha", message_id("m3")))

    # A send with no end: the envelope of the milter proves that Postfix took the mail.
    key = "it-" + uuid.uuid4().hex
    m4 = message_id("m4")
    await sent.reserve(login, key, "sha", m4)
    async with database.transaction() as conn:
        await conn.execute(
            "INSERT INTO mail_auth.envelope (message_id, sender, recipients)"
            " VALUES (%s, %s, %s)",
            (m4, f"{login}@mai.internal", ["codex@mai.internal"]),
        )
    show("accepted", await sent.reserve(login, key, "sha", message_id("m5")))

    # A send with no end and no envelope: Postfix did not take the mail.
    key = "it-" + uuid.uuid4().hex
    await sent.reserve(login, key, "sha", message_id("m6"))
    show("lost", await sent.reserve(login, key, "sha", message_id("m7")))


anyio.run(main)
