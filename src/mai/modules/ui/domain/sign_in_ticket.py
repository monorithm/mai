"""The sign-in tickets of the UI. A ticket is valid one time, for 60 s."""

from __future__ import annotations

import hashlib
import secrets
from abc import ABC, abstractmethod
from datetime import datetime, timedelta

TICKET_TTL = timedelta(seconds=60)


def new_ticket() -> str:
    return secrets.token_urlsafe(32)


def ticket_key(ticket: str) -> str:
    """The store keeps only this sha256 value, never the ticket."""
    return hashlib.sha256(ticket.encode()).hexdigest()


class SignInTicketStore(ABC):
    @abstractmethod
    async def save(self, key: str, expires_at: datetime) -> None: ...

    @abstractmethod
    async def take(self, key: str, now: datetime) -> bool:
        """Remove the ticket. Return True only if it existed and was not expired."""

    @abstractmethod
    async def clear(self) -> None: ...
