"""The storage port of the leases."""

from __future__ import annotations

import datetime as dt
from abc import ABC, abstractmethod

from .lease import Lease


class LeaseStore(ABC):
    @abstractmethod
    async def put(self, lease: Lease) -> None:
        """Insert or replace the lease with the same token hash."""

    @abstractmethod
    async def get(self, token_hash: str) -> Lease | None:
        """The lease of a token hash, or None."""

    @abstractmethod
    async def remove(self, token_hash: str) -> Lease | None:
        """Remove the lease of a token hash. Returns the removed lease or None."""

    @abstractmethod
    async def all(self) -> list[Lease]:
        """All leases, in session order. The leases with no session are last."""

    @abstractmethod
    async def seen(self, token_hash: str, now: dt.datetime) -> Lease | None:
        """Set the time of the last heartbeat of the stored lease to NOW.

        Returns the changed lease, or None when the store has no such lease.
        The change keeps each other field of the stored lease.
        """

    @abstractmethod
    async def bind(self, token_hash: str, session: str, handle: str) -> Lease | None:
        """Give the stored lease with no session the session SESSION and HANDLE.

        Returns the changed lease. Returns None when the store has no such
        lease, or when the lease has a session.
        """
