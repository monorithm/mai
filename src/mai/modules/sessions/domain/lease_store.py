"""The storage port of the leases."""

from __future__ import annotations

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
        """All leases, in session order."""
