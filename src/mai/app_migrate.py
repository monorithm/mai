"""Apply all migrations synchronously, then make the accounts.

The container controller runs this before Dovecot, Postfix and the app
start, because the daemons read the tables of mail_auth.
"""

import asyncio
import logging
import sys

from .platform.database import DEFAULT_DSN, migrate_sync

log = logging.getLogger("mai.migrate")


def all_migrations() -> list:
    from .app_module import feature_migrations

    return feature_migrations()


def migrate(dsn: str | None = None) -> list[int]:
    return migrate_sync(all_migrations(), dsn or DEFAULT_DSN)


async def _init_accounts() -> dict:
    from bustan import create_app_context

    from .app_module import AppModule
    from .modules.credentials import CredentialService

    context = create_app_context(AppModule)
    await context.init()
    try:
        return await context.get(CredentialService).init_accounts()
    finally:
        await context.close()


async def _grant_mailbox_access() -> dict:
    from bustan import create_app_context

    from .app_module import AppModule
    from .modules.credentials import CredentialService

    context = create_app_context(AppModule)
    await context.init()
    try:
        return await context.get(CredentialService).grant_mailbox_access()
    finally:
        await context.close()


def grant_mailbox_access() -> dict:
    """Give the mailbox rights. Run it only while Dovecot runs."""
    return asyncio.run(_grant_mailbox_access()) or {}


def init_accounts() -> dict:
    return asyncio.run(_init_accounts()) or {}


def main(argv: list[str] | None = None) -> int:
    logging.basicConfig(level=logging.INFO)
    applied = migrate()
    log.info("migrations applied: %s", applied or "none")
    if "--no-accounts" not in (argv or sys.argv[1:]):
        result = init_accounts()
        log.info("accounts created: %s", ", ".join(result.get("created", [])) or "none")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
