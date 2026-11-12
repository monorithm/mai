"""Keeps the UI state and the sign-in tickets in the memory of the process."""

from __future__ import annotations

from datetime import datetime

from bustan import Injectable

from ..domain.sign_in_ticket import SignInTicketStore
from ..domain.ui_state import UiStateStore


@Injectable()
class MemoryUiStateStore(UiStateStore):
    def __init__(self) -> None:
        self._enabled = False

    async def enabled(self) -> bool:
        return self._enabled

    async def set_enabled(self, enabled: bool) -> None:
        self._enabled = enabled


@Injectable()
class MemorySignInTicketStore(SignInTicketStore):
    def __init__(self) -> None:
        self._tickets: dict[str, datetime] = {}

    async def save(self, key: str, expires_at: datetime) -> None:
        self._tickets[key] = expires_at

    async def take(self, key: str, now: datetime) -> bool:
        expires_at = self._tickets.pop(key, None)
        for k in [k for k, v in self._tickets.items() if v <= now]:
            del self._tickets[k]
        return expires_at is not None and now < expires_at

    async def clear(self) -> None:
        self._tickets.clear()
