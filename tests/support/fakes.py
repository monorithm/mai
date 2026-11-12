"""Helpers for the tests."""

import time
from collections.abc import Callable


def wait_until(condition: Callable[[], bool], timeout: float = 2.0) -> None:
    """Poll condition each 10 ms until it is true; fail after timeout seconds."""
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if condition():
            return
        time.sleep(0.01)
    raise AssertionError("the condition did not become true in time")
