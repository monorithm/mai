"""The lane of long waits on HTTP: a full lane refuses a new wait at once with BUSY."""

import collections
import threading

from mai.platform.contract import ToolError

from .json_rpc import error_result, ok

LONG_POLL_TOOLS = frozenset({"wait_for_mail"})
WAITS_TOTAL = 64
WAITS_PER_LOGIN = 8


class WaitLane:
    """Counts the open long waits, in all and for each session handle."""

    def __init__(
        self, total: int = WAITS_TOTAL, per_key: int = WAITS_PER_LOGIN
    ) -> None:
        self.total, self.per_key = total, per_key
        self.open = 0
        self.of: collections.Counter[str] = collections.Counter()
        self._lock = threading.Lock()

    def enter(self, key: str) -> bool:
        with self._lock:
            if self.open >= self.total or self.of[key] >= self.per_key:
                return False
            self.open += 1
            self.of[key] += 1
            return True

    def leave(self, key: str) -> None:
        with self._lock:
            self.open -= 1
            self.of[key] -= 1
            if not self.of[key]:
                del self.of[key]

    @staticmethod
    def key_of(msg, headers) -> str | None:
        """The session key of a long-wait tool call, or None for each other message."""
        if not isinstance(msg, dict) or msg.get("method") != "tools/call":
            return None
        params = msg.get("params")
        if not isinstance(params, dict) or params.get("name") not in LONG_POLL_TOOLS:
            return None
        args = (
            params.get("arguments") if isinstance(params.get("arguments"), dict) else {}
        )
        return str(args.get("session") or headers.get("mai-session") or "")

    @staticmethod
    def busy(msg) -> dict:
        return ok(msg.get("id"), error_result(ToolError("BUSY")))
