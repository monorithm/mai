import importlib

from bustan import ConfigModule, HealthModule, Module

from .app_settings import AppSettings
from .platform.clock import ClockModule
from .platform.database import DatabaseModule
from .platform.health import HealthChecksModule

# The feature modules, in import order: (package, module class).
FEATURES = (
    ("credentials", "CredentialsModule"),
    ("sessions", "SessionsModule"),
    ("mail", "MailModule"),
    ("threads", "ThreadsModule"),
    ("notices", "NoticesModule"),
    ("board", "BoardModule"),
    ("mcp", "McpModule"),
    ("ui", "UiModule"),
    ("ops", "OpsModule"),
)


def feature_modules() -> list[type]:
    """Return the feature module classes. Each one must import."""
    found: list[type] = []
    for package, name in FEATURES:
        module = importlib.import_module(f"mai.modules.{package}")
        found.append(getattr(module, name))
    return found


def feature_migrations() -> list:
    """Return the MIGRATIONS of each feature module that declares them."""
    items: list = []
    for package, _ in FEATURES:
        module = importlib.import_module(f"mai.modules.{package}")
        items.extend(getattr(module, "MIGRATIONS", []))
    return items


# ConfigModule.for_root() reads .env and the environment when Python imports
# this file. HealthModule serves /health/live and /health/ready; the platform
# health module serves /healthz for the container health check.
PLATFORM_IMPORTS = [
    ConfigModule.for_root(env_file=".env", validation_schema=AppSettings),
    HealthModule.for_root(check_timeout=2.0),
    ClockModule,
    DatabaseModule,
    HealthChecksModule,
]


@Module(imports=[*PLATFORM_IMPORTS, *feature_modules()])
class AppModule:
    pass
