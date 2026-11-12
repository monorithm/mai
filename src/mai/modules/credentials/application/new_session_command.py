from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential import (
    ACL_RIGHTS,
    Principal,
    address,
    check_label,
    format_credential,
    iso,
    new_secret,
    parse_expiry,
)
from ..domain.credential_files import CredentialFiles
from ..domain.credential_store import CredentialStore
from ..domain.errors import AccountStateError, UnknownAccountError
from ..domain.mailbox_acl import MailboxAcl
from ..domain.secret_hasher import SecretHasher


@dataclass(frozen=True)
class NewSessionCommand:
    agent: str
    label: str
    expires_days: object = 7
    file: str | None = None


@Injectable()
class NewSessionHandler:
    """Makes the session login "<agent>.<label>" with a new credential and its queue rights."""

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

    async def execute(self, command: NewSessionCommand) -> dict:
        agent = await self._store.get(command.agent)
        if agent is None or agent.kind != "agent":
            raise UnknownAccountError(
                f"unknown agent: {command.agent}; add it first with"
                f" 'mai agent add {command.agent}'"
            )
        label = check_label(command.label)
        login = f"{command.agent}.{label}"
        if await self._store.get(login) is not None:
            raise AccountStateError(f"login exists: {login}")
        secret = new_secret()
        expires_at = datetime.now(UTC) + parse_expiry(command.expires_days)
        added = await self._store.add(
            Principal(
                login=login,
                kind="session",
                status="issued",
                agent=command.agent,
                label=label,
                password_hash=self._hasher.hash(secret),
                expires_at=expires_at,
            )
        )
        if not added:
            raise AccountStateError(f"login exists: {login}")
        acl = await self._acl.grant(command.agent, login, ACL_RIGHTS)
        credential = format_credential(login, secret)
        if command.file:
            self._files.write(command.file, credential)
        return {
            "login": login,
            "address": address(login),
            "credential": credential,
            "expires_at": iso(expires_at),
            "acl_set": acl,
            "file": str(command.file) if command.file else None,
        }
