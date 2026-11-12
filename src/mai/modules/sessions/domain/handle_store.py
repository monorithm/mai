"""The storage port of session handles. Only the SHA-256 of a handle is stored."""

from __future__ import annotations

import datetime as dt
from abc import ABC, abstractmethod


class HandleStore(ABC):
    @abstractmethod
    async def add(
        self, handle_sha: str, login: str, expires_at: dt.datetime
    ) -> None: ...

    @abstractmethod
    async def extend(
        self, handle_sha: str, expires_at: dt.datetime, now: dt.datetime
    ) -> str | None:
        """Move the expiry of a live handle to expires_at. Returns its login, or None
        when the handle is unknown or expired."""

    @abstractmethod
    async def end(self, handle_sha: str, now: dt.datetime) -> None:
        """End a handle. The application role has no DELETE right, so an end sets the expiry."""
