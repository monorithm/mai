"""A time limit for an operation, with the error code of the part that ran.

time_limit(seconds) raises ToolError when the operation takes more than
SECONDS. The code of the error tells the part of the server that ran at that
time. The default part is the store (STORE_UNAVAILABLE). A block in
time_limit_part(code) gives a different part, for example MAIL_SERVER_DOWN
around a call to the mail server.

A cancel scope that is outside a time_limit_part block, and inside the
time_limit, must not catch the cancellation of the time limit. No caller
does this.
"""

from __future__ import annotations

from collections.abc import Generator
from contextlib import contextmanager
from contextvars import ContextVar

import anyio

from .errors import ToolError

DEFAULT_PART = "STORE_UNAVAILABLE"
# The codes of the parts that run, the last one first.
_PARTS: ContextVar[list[str] | None] = ContextVar("time_limit_parts", default=None)


@contextmanager
def time_limit(seconds: float) -> Generator[None]:
    """Raise ToolError when the operation in the block takes more than SECONDS."""
    parts = [DEFAULT_PART]
    token = _PARTS.set(parts)
    try:
        with anyio.fail_after(seconds):
            yield
    except TimeoutError:
        raise ToolError(parts[-1]) from None
    finally:
        _PARTS.reset(token)


@contextmanager
def time_limit_part(code: str) -> Generator[None]:
    """Give CODE to a time limit that occurs during the block.

    Out of a time limit, the block has no effect.
    """
    parts = _PARTS.get()
    if parts is None:
        yield
        return
    parts.append(code)
    try:
        yield
    except anyio.get_cancelled_exc_class():
        # The time limit reads the code after this block, thus it stays.
        raise
    except BaseException:
        parts.pop()
        raise
    else:
        parts.pop()
