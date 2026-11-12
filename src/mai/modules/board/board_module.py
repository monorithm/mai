from functools import partial

from bustan import FactoryProvider, Module

from mai.modules.sessions import SessionsModule
from mai.modules.threads import ClaimService, ThreadsModule
from mai.platform.database import Database, DatabaseModule
from mai.platform.mailserver.mail_client_factory import MailClientFactory
from mai.platform.mailserver.mailserver_module import MailServerModule

from .application.board_tools import BoardTools
from .domain.board_store import BoardStore
from .infra.board_mailer import BoardMailer
from .infra.memory_board_store import MemoryBoardStore
from .infra.postgres_board_store import PostgresBoardStore


def _connect(dsn):
    import psycopg

    return psycopg.connect(dsn, autocommit=False)


def _board_store(
    database: Database, factory: MailClientFactory, claims: ClaimService
) -> BoardStore:
    if not database.is_postgres:
        return MemoryBoardStore()
    return PostgresBoardStore(
        partial(_connect, database.dsn), BoardMailer(factory), claims
    )


# The board binds its store port to the adapter that DATABASE_BACKEND selects. The PostgreSQL adapter
# sends board mail with the board credential through MailClientFactory, and claims item threads
# through the ClaimService of the threads module. The board tables are created by MIGRATIONS
# (versions 500 to 599), which app_migrate runs before the daemons start.
@Module(
    imports=[DatabaseModule, MailServerModule, SessionsModule, ThreadsModule],
    providers=[
        FactoryProvider(
            provide=BoardStore,
            use_factory=_board_store,
            inject=(Database, MailClientFactory, ClaimService),
        ),
        BoardTools,
    ],
    exports=[BoardTools],
)
class BoardModule:
    pass
