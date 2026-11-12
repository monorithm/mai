"""The memory adapter of the retry keys, for the memory backend and the tests."""

from __future__ import annotations

import anyio
from bustan import Injectable

from ..domain.errors import MailError
from ..domain.ports import SentRequests


@Injectable()
class InMemorySentRequests(SentRequests):
    def __init__(self) -> None:
        self._rows: dict[tuple[str, str], tuple[str, str]] = {}
        self._lock = anyio.Lock()

    async def reserve(self, login, request_id, content_sha, message_id):
        async with self._lock:
            row = self._rows.get((login, request_id))
            if row is not None:
                if row[0] != content_sha:
                    raise MailError("RETRY_KEY_REUSED", path="request_id")
                return row[1], True
            self._rows[(login, request_id)] = (content_sha, message_id)
            return message_id, False

    async def release(self, login, request_id, message_id):
        async with self._lock:
            row = self._rows.get((login, request_id))
            if row is not None and row[1] == message_id:
                del self._rows[(login, request_id)]
