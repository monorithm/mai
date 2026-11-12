from dataclasses import dataclass

from bustan import Injectable

from ..domain.credential_store import CredentialStore


@dataclass(frozen=True)
class GetRolesQuery:
    agent: str


@Injectable()
class GetRolesHandler:
    """The roles of an agent as project to role."""

    def __init__(self, store: CredentialStore) -> None:
        self._store = store

    async def execute(self, query: GetRolesQuery) -> dict[str, str]:
        return await self._store.roles(query.agent)
