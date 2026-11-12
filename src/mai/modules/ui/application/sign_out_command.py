"""Sign the owner out."""

from __future__ import annotations

from dataclasses import dataclass

from bustan import Injectable

from ..domain.owner_session import OwnerSessionStore


@dataclass(frozen=True, slots=True)
class SignOutCommand:
    key: str


@Injectable()
class SignOutHandler:
    def __init__(self, store: OwnerSessionStore) -> None:
        self._store = store

    async def execute(self, command: SignOutCommand) -> None:
        await self._store.delete(command.key)
