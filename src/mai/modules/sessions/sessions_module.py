from bustan import FactoryProvider, Module

from mai.modules.credentials import CredentialsModule
from mai.platform.database import Database, DatabaseModule
from mai.platform.mailserver import MailServerModule

from .api.session_tools import SessionTools
from .application.log_in_command import LogInHandler
from .application.log_out_command import LogOutHandler
from .application.resolve_session_query import ResolveSessionHandler
from .application.session_cache import SessionCache
from .application.session_service import SessionService
from .domain.handle_store import HandleStore
from .infra.in_memory_handle_store import InMemoryHandleStore
from .infra.postgres_handle_store import PostgresHandleStore


def _handle_store(database: Database) -> HandleStore:
    if database.is_postgres:
        return PostgresHandleStore(database)
    return InMemoryHandleStore()


# The module keeps the MCP session handles. DATABASE_BACKEND selects the
# adapter of the handle store. The migrations run in app_migrate.
@Module(
    imports=[DatabaseModule, MailServerModule, CredentialsModule],
    providers=[
        FactoryProvider(
            provide=HandleStore, use_factory=_handle_store, inject=(Database,)
        ),
        SessionCache,
        LogInHandler,
        ResolveSessionHandler,
        LogOutHandler,
        SessionService,
        SessionTools,
    ],
    exports=[SessionService, SessionTools],
)
class SessionsModule:
    pass
