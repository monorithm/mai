from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential import address, format_credential, iso, new_secret
from ..domain.credential_files import CredentialFiles
from ..domain.credential_store import CredentialStore
from ..domain.errors import AccountStateError, UnknownAccountError
from ..domain.secret_hasher import SecretHasher


@dataclass(frozen=True)
class RotateSessionCommand:
    login: str
    file: str | None = None


@Injectable()
class RotateSessionHandler:
    """Gives a session a new secret. The login, the mailboxes, and the claims stay."""

    def __init__(
        self, store: CredentialStore, hasher: SecretHasher, files: CredentialFiles
    ) -> None:
        self._store = store
        self._hasher = hasher
        self._files = files

    async def execute(self, command: RotateSessionCommand) -> dict:
        p = await self._store.get(command.login)
        if p is None or p.kind != "session":
            raise UnknownAccountError(f"unknown session login: {command.login}")
        if p.status in ("revoked", "expired"):
            raise AccountStateError(f"session is {p.status}: {command.login}")
        secret = new_secret()
        await self._store.set_hash(
            command.login, self._hasher.hash(secret), datetime.now(UTC)
        )
        credential = format_credential(command.login, secret)
        if command.file:
            self._files.write(command.file, credential)
        return {
            "login": command.login,
            "address": address(command.login),
            "credential": credential,
            "expires_at": iso(p.expires_at),
            "file": str(command.file) if command.file else None,
        }
