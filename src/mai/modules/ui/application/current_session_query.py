"""Find the live owner session of a cookie token."""

from __future__ import annotations

from dataclasses import dataclass

from bustan import Injectable

from mai.platform.clock import Clock

from ..domain.owner_session import OwnerSession, OwnerSessionStore, token_key


@dataclass(frozen=True, slots=True)
class CurrentSessionQuery:
    token: str | None


@Injectable()
class CurrentSessionHandler:
    def __init__(self, store: OwnerSessionStore, clock: Clock) -> None:
        self._store = store
        self._clock = clock

    async def execute(self, query: CurrentSessionQuery) -> OwnerSession | None:
        if not query.token:
            return None
        now = self._clock.now()
        session = await self._store.get(token_key(query.token))
        if session is None or not session.is_live(now):
            return None
        session = session.seen(now)
        await self._store.save(session)
        return session
