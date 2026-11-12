from __future__ import annotations

import asyncio
import datetime as dt
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

from ..domain.claim import Claim, ClaimStore, ClaimTransaction


def _utcnow() -> dt.datetime:
    return dt.datetime.now(dt.UTC)


class _MemoryTransaction(ClaimTransaction):
    def __init__(self, store: InMemoryClaimStore, agent: str, root: str) -> None:
        self._store = store
        self._key = (agent, root)
        self._pending: Claim | None | bool = False  # False: no change

    async def get(self) -> Claim | None:
        if isinstance(self._pending, Claim) or self._pending is None:
            return self._pending
        return self._store.rows.get(self._key)

    async def put(self, login: str, lease: dt.timedelta) -> Claim:
        claim = Claim(self._key[0], self._key[1], login, self._store.now() + lease)
        self._pending = claim
        return claim

    async def end(self) -> None:
        row = await self.get()
        if row is not None:
            self._pending = Claim(
                row.agent, row.root_msg_id, row.login, self._store.now()
            )

    async def now(self) -> dt.datetime:
        return self._store.now()

    def commit(self) -> None:
        if isinstance(self._pending, Claim):
            self._store.rows[self._key] = self._pending


class InMemoryClaimStore(ClaimStore):
    """Claims in a dict, for tests and the memory backend. One asyncio lock for each (agent, root)."""

    def __init__(self, now=_utcnow) -> None:
        # The logins that are revoked or expired, for tests of the session check.
        self.dead_sessions: set[str] = set()
        self.now = now
        self.rows: dict[tuple[str, str], Claim] = {}
        self.signalled: set[tuple[str, str, dt.datetime]] = set()
        self._locks: dict[tuple[str, str], asyncio.Lock] = {}

    @asynccontextmanager
    async def locked(self, agent: str, root: str) -> AsyncGenerator[ClaimTransaction]:
        lock = self._locks.setdefault((agent, root), asyncio.Lock())
        async with lock:
            txn = _MemoryTransaction(self, agent, root)
            yield txn
            txn.commit()

    async def roots_of(self, agent: str) -> list[str]:
        return [root for (a, root) in self.rows if a == agent]

    async def live_holder(self, agent: str, root: str) -> str | None:
        row = self.rows.get((agent, root))
        if row and row.live(self.now()) and row.login not in self.dead_sessions:
            return row.login
        return None

    async def session_live(self, login: str) -> bool:
        return login not in self.dead_sessions

    async def ended_unsignalled(self) -> list[Claim]:
        now = self.now()
        return [
            c
            for c in self.rows.values()
            if not c.live(now)
            and (c.agent, c.root_msg_id, c.lease_end) not in self.signalled
        ]

    async def mark_signalled(self, claim: Claim) -> bool:
        key = (claim.agent, claim.root_msg_id, claim.lease_end)
        if key in self.signalled:
            return False
        self.signalled.add(key)
        return True
