"""The storage port of accounts, aliases, and roles."""

from __future__ import annotations

from abc import ABC, abstractmethod
from datetime import datetime

from .credential import Principal


class CredentialStore(ABC):
    @abstractmethod
    async def get(self, login: str) -> Principal | None: ...

    @abstractmethod
    async def add(self, principal: Principal) -> bool:
        """Insert the principal. Returns False when the login exists."""

    @abstractmethod
    async def add_alias(self, alias: str, login: str) -> None:
        """Insert the alias when it does not exist."""

    @abstractmethod
    async def set_hash(self, login: str, password_hash: str, now: datetime) -> None:
        """Replace the hash. Session handles of the old secret stop working."""

    @abstractmethod
    async def set_status(self, login: str, status: str, now: datetime) -> None:
        """Change the status. A revoke also ends the handles and the claims of the login."""

    @abstractmethod
    async def mark_login(self, login: str, now: datetime) -> None:
        """Set the status active and the time of the last login."""

    @abstractmethod
    async def expire_old(self, now: datetime) -> None:
        """Mark each issued or active session whose expiry is past as expired."""

    @abstractmethod
    async def agents(self) -> list[Principal]:
        """The principals of kind agent, in login order."""

    @abstractmethod
    async def sessions(self, agent: str | None) -> list[Principal]:
        """The session principals, of one agent or of all, in login order."""

    @abstractmethod
    async def roles(self, agent: str) -> dict[str, str]:
        """The roles of an agent as project to role."""
