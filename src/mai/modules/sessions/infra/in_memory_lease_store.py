import datetime as dt

from ..domain.lease import Lease
from ..domain.lease_store import LeaseStore


class InMemoryLeaseStore(LeaseStore):
    """Keeps the leases in a dict of this process.

    A restart of the server ends all leases. The bridge then gets
    LEASE_UNKNOWN and asks for a new lease. Each operation has no await, thus
    it is atomic for the other tasks of the event loop.
    """

    def __init__(self) -> None:
        self._leases: dict[str, Lease] = {}

    async def put(self, lease: Lease) -> None:
        self._leases[lease.token_hash] = lease

    async def get(self, token_hash: str) -> Lease | None:
        return self._leases.get(token_hash)

    async def remove(self, token_hash: str) -> Lease | None:
        return self._leases.pop(token_hash, None)

    async def all(self) -> list[Lease]:
        return sorted(self._leases.values(), key=_order)

    async def seen(self, token_hash: str, now: dt.datetime) -> Lease | None:
        lease = self._leases.get(token_hash)
        if lease is None:
            return None
        lease = lease.seen(now)
        self._leases[token_hash] = lease
        return lease

    async def bind(self, token_hash: str, session: str, handle: str) -> Lease | None:
        lease = self._leases.get(token_hash)
        if lease is None or lease.session is not None:
            return None
        lease = lease.bound(session, handle)
        self._leases[token_hash] = lease
        return lease


def _order(lease: Lease) -> tuple[bool, str, dt.datetime]:
    return (lease.session is None, lease.session or "", lease.started_at)
