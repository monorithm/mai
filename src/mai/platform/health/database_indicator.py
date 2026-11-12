from bustan import HealthIndicatorResult, Injectable

from mai.platform.database import Database


@Injectable()
class DatabaseIndicator:
    """The readiness check of PostgreSQL. It runs SELECT 1.

    Each caller of the probe can read the detail of a failure, thus the
    detail never has the DSN or the error text.
    """

    name = "database"

    def __init__(self, database: Database) -> None:
        self._database = database

    async def check(self) -> HealthIndicatorResult:
        try:
            await self._database.fetch_one("SELECT 1")
        except Exception:  # noqa: BLE001 - any error means no data access
            return HealthIndicatorResult.down("the database does not answer")
        return HealthIndicatorResult.up()
