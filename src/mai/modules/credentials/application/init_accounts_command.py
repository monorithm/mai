from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential import (
    BOARD_ACL_RIGHTS,
    FOREVER,
    SYSTEM_ACCOUNTS,
    Principal,
    format_credential,
    new_secret,
    parse_credential,
)
from ..domain.credential_files import CredentialFiles
from ..domain.credential_store import CredentialStore
from ..domain.errors import CredentialError
from ..domain.mailbox_acl import MailboxAcl
from ..domain.secret_hasher import SecretHasher


@dataclass(frozen=True)
class InitAccountsCommand:
    write_secrets: bool = True


@Injectable()
class InitAccountsHandler:
    """Makes the system accounts owner and board. "mai agent add" makes the agents.

    The command is idempotent. The board secret goes to
    <secrets dir>/board.cred with mode 0600 when they do not exist.
    """

    def __init__(
        self,
        store: CredentialStore,
        hasher: SecretHasher,
        files: CredentialFiles,
        acl: MailboxAcl,
    ) -> None:
        self._store = store
        self._hasher = hasher
        self._files = files
        self._acl = acl

    async def execute(self, command: InitAccountsCommand) -> dict:
        now = datetime.now(UTC)
        made = []
        for login, kind in SYSTEM_ACCOUNTS:
            account = Principal(
                login=login, kind=kind, status="active", expires_at=FOREVER
            )
            if await self._store.add(account):
                made.append(login)
        if command.write_secrets:
            # The owner key comes from the host through "mai owner set-key".
            # Only the board secret goes to a file.
            await self._system_secret("board", now)
        # The board account reads the queue of each agent that exists.
        agents = [a.login for a in await self._store.agents()]
        for agent in agents:
            await self._acl.grant(agent, "board", BOARD_ACL_RIGHTS)
        return {"created": made, "agents": agents}

    async def _system_secret(self, login: str, now: datetime) -> None:
        path = self._files.system_path(login)
        secret = None
        text = self._files.read(path)
        if text:
            try:
                file_login, secret = parse_credential(text)
                if file_login != login:
                    secret = None
            except CredentialError:
                secret = None
        account = await self._store.get(login)
        stored = account.password_hash if account else None
        if secret is None or not self._hasher.verify(secret, stored):
            if secret is None:
                secret = new_secret()
                self._files.write(path, format_credential(login, secret))
            await self._store.set_hash(login, self._hasher.hash(secret), now)
