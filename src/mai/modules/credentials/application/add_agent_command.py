"""Makes an agent account: the shared queue that the sessions of the agent read."""

from dataclasses import dataclass

from bustan import Injectable

from ..domain.credential import BOARD_ACL_RIGHTS, Principal, address, check_agent_name
from ..domain.credential_store import CredentialStore
from ..domain.errors import AccountStateError
from ..domain.mailbox_acl import MailboxAcl


@dataclass(frozen=True)
class AddAgentCommand:
    name: str


@Injectable()
class AddAgentHandler:
    """Adds a principal of kind agent with the status active.

    The command is idempotent for an agent that exists. It also gives the
    board account the rights lookup and read on the agent INBOX, thus the
    router can read the queue. A login of a different kind with the same
    name is an error.
    """

    def __init__(self, store: CredentialStore, acl: MailboxAcl) -> None:
        self._store = store
        self._acl = acl

    async def execute(self, command: AddAgentCommand) -> dict:
        name = check_agent_name(command.name)
        created = await self._store.add(
            Principal(login=name, kind="agent", status="active")
        )
        if not created:
            current = await self._store.get(name)
            if current is None or current.kind != "agent":
                raise AccountStateError(f"login exists and is not an agent: {name}")
        acl = await self._acl.grant(name, "board", BOARD_ACL_RIGHTS)
        return {
            "agent": name,
            "address": address(name),
            "created": created,
            "acl_set": acl,
        }
