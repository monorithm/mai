from datetime import datetime

from ..domain.agent_key_store import AgentKeyStore


class InMemoryAgentKeyStore(AgentKeyStore):
    """Keeps the hashes of the agent keys in a dict of this process."""

    def __init__(self) -> None:
        self._hashes: dict[str, str] = {}

    async def get_hash(self, agent: str) -> str | None:
        return self._hashes.get(agent)

    async def set_hash(self, agent: str, key_hash: str, now: datetime) -> None:
        self._hashes[agent] = key_hash

    async def remove(self, agent: str) -> bool:
        return self._hashes.pop(agent, None) is not None
