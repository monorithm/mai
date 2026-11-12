"""Gives the mailbox rights that the mail server must have, after Dovecot starts.

Dovecot keeps the ACL of a mailbox, thus a grant succeeds only while Dovecot
runs. The board account must read each agent queue, else the router sees an
empty queue. The owner must read the INBOX of the account copies. Each live session must have its rights on the queue of its agent.
The command gives all these rights again. A grant that is already there does
not change, thus the command is idempotent and repairs a grant that failed.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential import (
    ACL_RIGHTS,
    BOARD_ACL_RIGHTS,
    COPIES_ACL_RIGHTS,
    COPIES_LOGIN,
)
from ..domain.credential_store import CredentialStore
from ..domain.mailbox_acl import MailboxAcl

log = logging.getLogger("mai.credentials")

LIVE_STATUSES = ("issued", "active")


@dataclass(frozen=True)
class GrantMailboxAccessCommand:
    pass


@Injectable()
class GrantMailboxAccessHandler:
    def __init__(self, store: CredentialStore, acl: MailboxAcl) -> None:
        self._store = store
        self._acl = acl

    async def execute(self, command: GrantMailboxAccessCommand) -> dict:
        now = datetime.now(UTC)
        granted, failed = [], []
        await self._one(COPIES_LOGIN, "owner", COPIES_ACL_RIGHTS, granted, failed)
        for agent in await self._store.agents():
            await self._one(agent.login, "board", BOARD_ACL_RIGHTS, granted, failed)
        for session in await self._store.sessions(None):
            if session.status not in LIVE_STATUSES or session.is_expired(now):
                continue
            if session.agent:
                await self._one(
                    session.agent, session.login, ACL_RIGHTS, granted, failed
                )
        for agent, login in failed:
            log.warning(
                "the mailbox grant for %s on the queue of %s failed", login, agent
            )
        return {"granted": granted, "failed": failed}

    async def _one(self, agent, login, rights, granted, failed) -> None:
        if await self._acl.grant(agent, login, rights):
            granted.append((agent, login))
        else:
            failed.append((agent, login))
