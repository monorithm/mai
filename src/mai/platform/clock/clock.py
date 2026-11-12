from datetime import UTC, datetime

from bustan import Injectable


@Injectable()
class Clock:
    """Gives the current time in UTC.

    Inject the clock and do not call datetime.now() directly. A test can then
    replace the clock and get a known time.
    """

    def now(self) -> datetime:
        return datetime.now(UTC)
