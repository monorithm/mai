from dataclasses import dataclass

from bustan import Injectable

from ..domain.credential_store import CredentialStore


@dataclass(frozen=True)
class GetAccountKindQuery:
    login: str


@Injectable()
class GetAccountKindHandler:
    """The kind of an account (agent, session, owner, or system), or None when it does not exist."""

    def __init__(self, store: CredentialStore) -> None:
        self._store = store

    async def execute(self, query: GetAccountKindQuery) -> str | None:
        p = await self._store.get(query.login)
        return None if p is None else p.kind
