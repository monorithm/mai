"""The storage port of the agent keys. The store keeps only a hash of each key."""

from __future__ import annotations

from abc import ABC, abstractmethod
from datetime import datetime


class AgentKeyStore(ABC):
    @abstractmethod
    async def get_hash(self, agent: str) -> str | None:
        """The hash of the key of AGENT, or None when the agent has no key."""

    @abstractmethod
    async def set_hash(self, agent: str, key_hash: str, now: datetime) -> None:
        """Insert or replace the hash of the key of AGENT."""

    @abstractmethod
    async def remove(self, agent: str) -> bool:
        """Remove the key of AGENT. Returns False when the agent has no key.

        After the removal, get_hash gives None. The store can keep a row with
        no hash, because the app role has no DELETE right.
        """
