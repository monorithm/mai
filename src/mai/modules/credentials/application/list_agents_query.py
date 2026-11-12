"""Lists the agent accounts of the store."""

from dataclasses import dataclass

from bustan import Injectable

from ..domain.credential import address
from ..domain.credential_store import CredentialStore


@dataclass(frozen=True)
class ListAgentsQuery:
    active_only: bool = False


@Injectable()
class ListAgentsHandler:
    """The agents in login order, with the count of the live sessions of each."""

    def __init__(self, store: CredentialStore) -> None:
        self._store = store

    async def execute(self, query: ListAgentsQuery) -> list[dict]:
        agents = await self._store.agents()
        if query.active_only:
            agents = [a for a in agents if a.status == "active"]
        sessions = await self._store.sessions(None)
        rows = []
        for a in agents:
            live = [
                s
                for s in sessions
                if s.agent == a.login and s.status in ("issued", "active")
            ]
            rows.append(
                {
                    "agent": a.login,
                    "address": address(a.login),
                    "status": a.status,
                    "sessions": len(live),
                }
            )
        return rows
