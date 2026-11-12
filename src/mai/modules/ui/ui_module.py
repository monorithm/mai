"""The ui module: the owner API /api/ui and the owner pages under /ui."""

from __future__ import annotations

from bustan import ClassProvider, FactoryProvider, Module

from mai.modules.board import BoardModule
from mai.modules.credentials import CredentialsModule
from mai.platform.clock import ClockModule
from mai.platform.database import Database, DatabaseModule

from .api.ui_api_controller import UiApiController
from .api.ui_controller import UiController
from .application.board_snapshot_query import BoardSnapshotHandler
from .application.current_session_query import CurrentSessionHandler
from .application.manage_sessions_command import ManageSessionsHandler
from .application.sign_out_command import SignOutHandler
from .application.ui_access_service import UiAccessService
from .domain.mail_view import EnvelopeLookup, MailReader, ThreadLookup
from .domain.owner_session import OwnerSessionStore
from .domain.sign_in_ticket import SignInTicketStore
from .domain.ui_state import UiStateStore
from .infra.doveadm_mail_reader import DoveadmMailReader
from .infra.memory_owner_session_store import MemoryOwnerSessionStore
from .infra.memory_ui_state import MemorySignInTicketStore, MemoryUiStateStore
from .infra.postgres_envelope_lookup import PostgresEnvelopeLookup, PostgresThreadLookup
from .infra.postgres_ui_state import PostgresUiStateStore


def _ui_state(database: Database) -> UiStateStore:
    if not database.is_postgres:
        return MemoryUiStateStore()
    return PostgresUiStateStore(database)


def _envelopes(database: Database) -> EnvelopeLookup:
    return PostgresEnvelopeLookup(database)


def _threads(database: Database) -> ThreadLookup:
    return PostgresThreadLookup(database)


# The UI state lives in the table ui.state (versions 700 to 709) on PostgreSQL.
# The sign-in tickets and the cookie sessions live in the memory of the process:
# a restart signs the owner out.
@Module(
    imports=[ClockModule, CredentialsModule, BoardModule, DatabaseModule],
    controllers=[UiApiController, UiController],
    providers=[
        FactoryProvider(
            provide=UiStateStore, use_factory=_ui_state, inject=(Database,)
        ),
        FactoryProvider(
            provide=EnvelopeLookup, use_factory=_envelopes, inject=(Database,)
        ),
        FactoryProvider(provide=ThreadLookup, use_factory=_threads, inject=(Database,)),
        ClassProvider(provide=SignInTicketStore, use_class=MemorySignInTicketStore),
        ClassProvider(provide=OwnerSessionStore, use_class=MemoryOwnerSessionStore),
        ClassProvider(provide=MailReader, use_class=DoveadmMailReader),
        UiAccessService,
        SignOutHandler,
        CurrentSessionHandler,
        ManageSessionsHandler,
        BoardSnapshotHandler,
    ],
)
class UiModule:
    pass
