import datetime as dt

from ..domain.handle_store import HandleStore


class InMemoryHandleStore(HandleStore):
    """Keeps the handles in a dict of this process. For tests and the memory backend."""

    def __init__(self) -> None:
        self._rows: dict[str, tuple[str, dt.datetime]] = {}

    async def add(self, handle_sha: str, login: str, expires_at: dt.datetime) -> None:
        self._rows[handle_sha] = (login, expires_at)

    async def extend(
        self, handle_sha: str, expires_at: dt.datetime, now: dt.datetime
    ) -> str | None:
        row = self._rows.get(handle_sha)
        if row is None or row[1] <= now:
            return None
        self._rows[handle_sha] = (row[0], expires_at)
        return row[0]

    async def end(self, handle_sha: str, now: dt.datetime) -> None:
        row = self._rows.get(handle_sha)
        if row is not None:
            self._rows[handle_sha] = (row[0], now)
