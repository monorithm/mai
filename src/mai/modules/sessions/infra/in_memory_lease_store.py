from ..domain.lease import Lease
from ..domain.lease_store import LeaseStore


class InMemoryLeaseStore(LeaseStore):
    """Keeps the leases in a dict of this process.

    A restart of the server ends all leases. The bridge then gets
    LEASE_UNKNOWN and asks for a new lease.
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
        return sorted(self._leases.values(), key=lambda lease: lease.session)
