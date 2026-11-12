"""Sets the owner key. The CLI of the host makes the key, and the server keeps only a hash."""

from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential import FOREVER, Principal, parse_credential
from ..domain.credential_store import CredentialStore
from ..domain.errors import AccountStateError, CredentialError
from ..domain.secret_hasher import SecretHasher


@dataclass(frozen=True)
class SetOwnerKeyCommand:
    key: str
    replace: bool = False


@Injectable()
class SetOwnerKeyHandler:
    """Stores the hash of the owner key "mai1:owner:<secret>".

    Raises ValueError for a key of a bad form, and AccountStateError when a
    key exists and replace is False.
    """

    def __init__(self, store: CredentialStore, hasher: SecretHasher) -> None:
        self._store = store
        self._hasher = hasher

    async def execute(self, command: SetOwnerKeyCommand) -> dict:
        try:
            login, secret = parse_credential(command.key)
        except CredentialError:
            raise ValueError("the key must have the form mai1:owner:<secret>") from None
        if login != "owner":
            raise ValueError("the key must have the form mai1:owner:<secret>")
        now = datetime.now(UTC)
        await self._store.add(
            Principal(login="owner", kind="owner", status="active", expires_at=FOREVER)
        )
        current = await self._store.get("owner")
        if current is not None and current.password_hash and not command.replace:
            raise AccountStateError("an owner key exists")
        await self._store.set_hash("owner", self._hasher.hash(secret), now)
        return {"owner_key": "set"}
