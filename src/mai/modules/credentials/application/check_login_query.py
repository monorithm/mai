from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential_store import CredentialStore
from .authenticate_command import fingerprint


@dataclass(frozen=True)
class CheckLoginQuery:
    login: str


@Injectable()
class CheckLoginHandler:
    """Returns the fingerprint of the secret of a login that can still work, else None.

    A session handle stays valid only while this value is the same as at login.
    """

    def __init__(self, store: CredentialStore) -> None:
        self._store = store

    async def execute(self, query: CheckLoginQuery) -> str | None:
        p = await self._store.get(query.login)
        if p is None or p.status not in ("issued", "active"):
            return None
        if p.is_expired(datetime.now(UTC)):
            return None
        return fingerprint(p.password_hash)
