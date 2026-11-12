from bustan import Module

from mai.platform.database import DatabaseModule

from .database_indicator import DatabaseIndicator
from .health_checks_module import HealthChecks
from .healthz_controller import HealthzController


# The root module also imports HealthModule.for_root(), which is global and
# gives HealthService.
@Module(
    imports=[DatabaseModule],
    controllers=[HealthzController],
    providers=[DatabaseIndicator, HealthChecks],
    exports=[HealthChecks],
)
class HealthChecksModule:
    pass
