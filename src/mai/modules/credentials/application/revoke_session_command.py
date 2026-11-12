import logging
from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential_store import CredentialStore
from ..domain.errors import UnknownAccountError
from ..domain.mailbox_acl import MailboxAcl

log = logging.getLogger("mai.credentials")


@dataclass(frozen=True)
class RevokeSessionCommand:
    login: str


@Injectable()
class RevokeSessionHandler:
    """Revokes a session login, ends its handles and claims, and removes its queue rights."""

    def __init__(self, store: CredentialStore, acl: MailboxAcl) -> None:
        self._store = store
        self._acl = acl

    async def execute(self, command: RevokeSessionCommand) -> dict:
        p = await self._store.get(command.login)
        if p is None or p.kind != "session":
            raise UnknownAccountError(f"unknown session login: {command.login}")
        await self._store.set_status(command.login, "revoked", datetime.now(UTC))
        if p.agent:
            # The unread mail of the session goes back to the agent queue, thus a
            # different session of the agent gets it. This needs the queue rights.
            if await self._acl.return_unread(p.agent, command.login):
                await self._acl.remove(p.agent, command.login)
            else:
                # The rights stay: the passdb refuses the login, and the stale job can return the mail.
                log.warning(
                    "the unread mail of %s did not go back to the queue; its queue rights stay",
                    command.login,
                )
        return {"login": command.login, "status": "revoked"}
