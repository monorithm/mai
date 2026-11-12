from bustan import HealthService, Injectable

from mai.platform.database import Database

from .database_indicator import DatabaseIndicator


@Injectable()
class HealthChecks:
    """Registers the readiness check of the database, on PostgreSQL only.

    A failed dependency must not restart a process that works, thus the check
    goes to readiness only.
    """

    def __init__(
        self, health: HealthService, database: Database, indicator: DatabaseIndicator
    ) -> None:
        self._health = health
        self._database = database
        self._indicator = indicator

    def on_module_init(self) -> None:
        if self._database.is_postgres:
            self._health.register_readiness(self._indicator)

    async def ready(self) -> bool:
        if not self._database.is_postgres:
            return True
        return (await self._indicator.check()).status.value == "up"
