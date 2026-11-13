"""The memory adapter of the retry keys, for the memory backend and the tests."""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass

import anyio
from bustan import Injectable

from ..domain.errors import MailError
from ..domain.ports import Reservation, SentRequests


@dataclass(slots=True)
class _Row:
    content_sha: str
    message_id: str
    sent_at: dt.datetime | None = None


@Injectable()
class InMemorySentRequests(SentRequests):
    def __init__(self) -> None:
        self._rows: dict[tuple[str, str], _Row] = {}
        self._lock = anyio.Lock()

    async def reserve(self, login, request_id, content_sha, message_id):
        async with self._lock:
            row = self._rows.get((login, request_id))
            if row is not None and row.content_sha != content_sha:
                raise MailError("RETRY_KEY_REUSED", path="request_id")
            if row is not None and row.sent_at is not None:
                return Reservation(row.message_id, True, row.sent_at)
            # A new key, or a key whose mail did not go out: this mail takes it.
            self._rows[(login, request_id)] = _Row(content_sha, message_id)
            return Reservation(message_id, False)

    async def mark_sent(self, login, request_id, message_id, sent_at):
        async with self._lock:
            row = self._rows.get((login, request_id))
            if row is not None and row.message_id == message_id:
                row.sent_at = sent_at

    async def release(self, login, request_id, message_id):
        # A row with no date is free for a retry: nothing changes.
        return None
