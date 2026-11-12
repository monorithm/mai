from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential_store import CredentialStore


@dataclass(frozen=True)
class ListSessionsQuery:
    agent: str | None = None


@Injectable()
class ListSessionsHandler:
    """Lists the session logins with no hash. Past expiries change to expired first."""

    def __init__(self, store: CredentialStore) -> None:
        self._store = store

    async def execute(self, query: ListSessionsQuery) -> list[dict]:
        await self._store.expire_old(datetime.now(UTC))
        return [p.public() for p in await self._store.sessions(query.agent)]
