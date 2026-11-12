"""The claim of a thread: a lease that gives one session the mail of one thread of an agent queue."""

from __future__ import annotations

import abc
import datetime as dt
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass

LEASE = dt.timedelta(minutes=30)


def queue_of(agent: str) -> str:
    """The mailbox name of the agent queue, as a session of that agent sees it."""
    return f"shared/{agent}/INBOX"


def iso(value: dt.datetime | None) -> str | None:
    if value is None:
        return None
    if value.tzinfo is None:
        value = value.replace(tzinfo=dt.UTC)
    return value.astimezone(dt.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


@dataclass(frozen=True, slots=True)
class Claim:
    agent: str
    root_msg_id: str
    login: str
    lease_end: dt.datetime

    def live(self, now: dt.datetime) -> bool:
        return self.lease_end > now


class ClaimTransaction(abc.ABC):
    """The operations on one claim row while the lock of (agent, root) is held."""

    @abc.abstractmethod
    async def get(self) -> Claim | None: ...

    @abc.abstractmethod
    async def put(self, login: str, lease: dt.timedelta) -> Claim: ...

    @abc.abstractmethod
    async def end(self) -> None: ...

    @abc.abstractmethod
    async def now(self) -> dt.datetime: ...


class ClaimStore(abc.ABC):
    """The port of the claim rows (mail_auth.thread_claim)."""

    @abc.abstractmethod
    def locked(
        self, agent: str, root: str
    ) -> AbstractAsyncContextManager[ClaimTransaction]:
        """Hold the lock of (agent, root) for the block. A block that raises keeps no change."""

    @abc.abstractmethod
    async def roots_of(self, agent: str) -> list[str]: ...

    @abc.abstractmethod
    async def live_holder(self, agent: str, root: str) -> str | None:
        """The session of the live claim, only if that session can log in."""

    @abc.abstractmethod
    async def session_live(self, login: str) -> bool:
        """True when the session is issued or active and did not expire."""

    @abc.abstractmethod
    async def ended_unsignalled(self) -> list[Claim]:
        """Give each claim whose lease ended and that has no stale signal yet."""

    @abc.abstractmethod
    async def mark_signalled(self, claim: Claim) -> bool:
        """Record the stale signal of this lease. False when a different process recorded it first."""
