from bustan import ClassProvider, FactoryProvider, Module

from mai.modules.credentials import CredentialsModule
from mai.modules.sessions import SessionsModule
from mai.platform.database import Database, DatabaseModule
from mai.platform.mailserver import MailServerModule

from .api.thread_tools import ThreadTools
from .application.claim_service import ClaimService
from .application.queue_router import QueueRouter
from .domain.claim import ClaimStore
from .domain.credentials import CredentialReader
from .domain.mover import QueueMover
from .domain.returner import ThreadMailReturner
from .infra.credential_file import FileCredentialReader
from .infra.doveadm_mover import DoveadmMover
from .infra.doveadm_returner import DoveadmReturner
from .infra.in_memory_claim_store import InMemoryClaimStore
from .infra.postgres_claim_store import PostgresClaimStore
from .jobs.session_stale_job import SessionStaleJob


def _claim_store(database: Database) -> ClaimStore:
    if database.is_postgres:
        return PostgresClaimStore(database)
    return InMemoryClaimStore()


# DATABASE_BACKEND selects the adapter of ClaimStore. The router starts its
# queue workers in on_application_bootstrap, looks for new agents each
# AGENT_REFRESH seconds, and stops them in
# on_application_shutdown; the session_stale job runs each minute.
# They run only in the HTTP server process, which sets MAI_BACKGROUND=on.
# THREADS_ROUTER=off and THREADS_STALE_SIGNAL=off also turn them off there.
@Module(
    imports=[DatabaseModule, MailServerModule, SessionsModule, CredentialsModule],
    providers=[
        FactoryProvider(
            provide=ClaimStore, use_factory=_claim_store, inject=(Database,)
        ),
        ClassProvider(provide=QueueMover, use_class=DoveadmMover),
        ClassProvider(provide=ThreadMailReturner, use_class=DoveadmReturner),
        ClassProvider(provide=CredentialReader, use_class=FileCredentialReader),
        ClaimService,
        ThreadTools,
        QueueRouter,
        SessionStaleJob,
    ],
    exports=[ThreadTools, ClaimService, CredentialReader],
)
class ThreadsModule:
    pass
