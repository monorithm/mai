from bustan import ClassProvider, FactoryProvider, Module

from mai.modules.credentials import CredentialsModule
from mai.platform.database import Database, DatabaseModule
from mai.platform.mailserver import MailServerModule

from .api.leases_controller import AgentLeasesController, LeasesController
from .api.session_tools import SessionTools
from .api.sessions_controller import SessionsController
from .application.lease_service import LeaseService
from .application.log_in_command import LogInHandler
from .application.log_out_command import LogOutHandler
from .application.owner_sessions import OwnerSessions
from .application.resolve_session_query import ResolveSessionHandler
from .application.session_cache import SessionCache
from .application.session_service import SessionService
from .domain.handle_store import HandleStore
from .domain.lease_store import LeaseStore
from .domain.new_mail_counter import NewMailCounter
from .infra.imap_new_mail_counter import ImapNewMailCounter
from .infra.in_memory_handle_store import InMemoryHandleStore
from .infra.in_memory_lease_store import InMemoryLeaseStore
from .infra.postgres_handle_store import PostgresHandleStore


def _handle_store(database: Database) -> HandleStore:
    if database.is_postgres:
        return PostgresHandleStore(database)
    return InMemoryHandleStore()


# The module keeps the leases, the MCP session handles, and the owner API of
# the sessions. DATABASE_BACKEND selects the adapter of the handle store. The
# migrations run in app_migrate.
@Module(
    imports=[DatabaseModule, MailServerModule, CredentialsModule],
    controllers=[LeasesController, SessionsController, AgentLeasesController],
    providers=[
        ClassProvider(provide=LeaseStore, use_class=InMemoryLeaseStore),
        ClassProvider(provide=NewMailCounter, use_class=ImapNewMailCounter),
        LeaseService,
        OwnerSessions,
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
    exports=[SessionService, SessionTools, LeaseService, OwnerSessions],
)
class SessionsModule:
    pass
