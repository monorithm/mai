"""Keeps the UI sign-in sessions in the memory of the process.

A restart signs the owner out, which is the intended behavior for a
short-lived admin session.
"""

from __future__ import annotations

from datetime import datetime

from bustan import Injectable

from ..domain.owner_session import OwnerSession, OwnerSessionStore


@Injectable()
class MemoryOwnerSessionStore(OwnerSessionStore):
    def __init__(self) -> None:
        self._sessions: dict[str, OwnerSession] = {}

    async def save(self, session: OwnerSession) -> None:
        self._sessions[session.key] = session

    async def get(self, key: str) -> OwnerSession | None:
        return self._sessions.get(key)

    async def delete(self, key: str) -> None:
        self._sessions.pop(key, None)

    async def purge_expired(self, now: datetime) -> None:
        for k in [k for k, s in self._sessions.items() if not s.is_live(now)]:
            del self._sessions[k]

    async def clear(self) -> None:
        self._sessions.clear()
