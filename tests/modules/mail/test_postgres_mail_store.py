"""The PostgreSQL adapters of the mail module against a real database. Set MAI_IT and DATABASE_DSN to run."""

import os
import uuid

import anyio
import pytest
from bustan import ConfigService

pytestmark = pytest.mark.skipif(
    not os.environ.get("MAI_IT"), reason="needs PostgreSQL (MAI_IT)"
)


class _Config(ConfigService):
    def __init__(self):
        super().__init__({})

    def get(self, key: str, default=None):
        return {
            "DATABASE_BACKEND": "postgres",
            "DATABASE_DSN": os.environ.get("DATABASE_DSN"),
        }.get(key, default)


def test_retry_keys_and_directory():
    from mai.modules.mail.domain.errors import MailError
    from mai.modules.mail.infra.postgres_mail_store import (
        MIGRATIONS,
        PostgresDirectory,
        PostgresSentRequests,
    )
    from mai.platform.database import Database

    database = Database(_Config())
    login = "it-" + uuid.uuid4().hex[:8]

    async def scenario():
        await database.migrate(MIGRATIONS)
        async with database.transaction() as conn:
            await conn.execute(
                "INSERT INTO mail_auth.principal (login, kind) VALUES (%s, 'agent')",
                (login,),
            )
        sent = PostgresSentRequests(database)
        assert await sent.reserve(login, "r1", "a", "<m1@x>") == ("<m1@x>", False)
        assert await sent.reserve(login, "r1", "a", "<m2@x>") == ("<m1@x>", True)
        with pytest.raises(MailError):
            await sent.reserve(login, "r1", "b", "<m3@x>")
        await sent.release(login, "r1", "<m1@x>")
        assert await sent.reserve(login, "r1", "b", "<m4@x>") == ("<m4@x>", False)
        directory = PostgresDirectory(database)
        assert await directory.known_logins([login, "nobody-here"]) == {login}
        assert login in await directory.agents()

    anyio.run(scenario)
