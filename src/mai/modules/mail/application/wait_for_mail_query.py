"""The tool wait_for_mail: a long wait for new mail in the inbox and the agent queue."""

from __future__ import annotations

import time
from contextlib import contextmanager

import anyio
from bustan import Injectable

from ..domain import mail_rules as r
from ..domain.errors import MailError
from .mail_reader import MailReader, boxes_of, by_date


@Injectable()
class WaitSlots:
    """Limits on concurrent long waits: in total and for each login."""

    def __init__(self) -> None:
        self._count: dict[str, int] = {}
        self._total = 0

    @contextmanager
    def slot(self, login: str):
        if self._total >= r.WAIT_TOTAL or self._count.get(login, 0) >= r.WAIT_PER_LOGIN:
            raise MailError("BUSY")
        self._total += 1
        self._count[login] = self._count.get(login, 0) + 1
        try:
            yield
        finally:
            self._total -= 1
            self._count[login] -= 1


@Injectable()
class WaitForMailHandler:
    def __init__(self, reader: MailReader, slots: WaitSlots) -> None:
        self._reader = reader
        self._slots = slots

    async def _fresh(self, state) -> list[dict]:
        entries = await self._reader.scan(
            state, boxes_of(state), f"UNSEEN UNKEYWORD {r.NOTIFIED}"
        )
        return [
            e
            for e in entries
            if r.SEEN not in e["flags"] and r.NOTIFIED not in e["flags"]
        ]

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        timeout = args.get("timeout_sec", r.MAX_WAIT)
        if (
            isinstance(timeout, bool)
            or not isinstance(timeout, (int, float))
            or timeout < 0
            or timeout > r.MAX_WAIT
        ):
            raise MailError("INVALID_ARGUMENT", path="timeout_sec")
        deadline = time.monotonic() + timeout
        with self._slots.slot(state.login):
            async with self._reader.lock(state):
                while True:
                    found = by_date(await self._fresh(state), newest_first=False)[:20]
                    if found:
                        by_box: dict[str, list] = {}
                        for e in found:
                            by_box.setdefault(e["mailbox"], []).append(e["uid"])
                        for box, uids in by_box.items():
                            await self._reader.call(
                                state, "store_flags", box, uids, add=(r.NOTIFIED,)
                            )
                        for e in found:
                            e["flags"].append(r.NOTIFIED)
                        return {
                            "notice": r.NOTICE,
                            "address": state.address,
                            "messages": [self._reader.summary(state, e) for e in found],
                            "timed_out": False,
                        }
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return {
                            "notice": r.NOTICE,
                            "address": state.address,
                            "messages": [],
                            "timed_out": True,
                        }
                    started = time.monotonic()
                    got = await self._reader.call(
                        state, "idle", boxes_of(state), remaining
                    )
                    if not got and time.monotonic() - started < 0.05:
                        # An IDLE that returns at once with no mail must not spin the loop.
                        await anyio.sleep(
                            min(0.5, max(0.0, deadline - time.monotonic()))
                        )
