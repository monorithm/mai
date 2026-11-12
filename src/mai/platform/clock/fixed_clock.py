from datetime import UTC, datetime, timedelta

from .clock import Clock

DEFAULT_START = datetime(2026, 1, 1, 9, 0, tzinfo=UTC)


class FixedClock(Clock):
    """A clock that gives a known time, for tests.

    The time changes only when the caller calls advance(). Give an instance as
    a provider override for Clock.
    """

    def __init__(self, start: datetime = DEFAULT_START) -> None:
        self._now = start

    def now(self) -> datetime:
        return self._now

    def advance(self, delta: timedelta = timedelta(minutes=1)) -> datetime:
        self._now += delta
        return self._now
