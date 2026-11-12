"""The ui module: the owner pages under /ui."""

from __future__ import annotations

from bustan import ClassProvider, Module

from mai.modules.board import BoardModule
from mai.modules.credentials import CredentialsModule
from mai.platform.clock import ClockModule

from .api.ui_controller import UiController
from .application.board_snapshot_query import BoardSnapshotHandler
from .application.current_session_query import CurrentSessionHandler
from .application.manage_sessions_command import ManageSessionsHandler
from .application.sign_in_command import SignInHandler
from .application.sign_out_command import SignOutHandler
from .domain.owner_session import OwnerSessionStore
from .infra.memory_owner_session_store import MemoryOwnerSessionStore


@Module(
    imports=[ClockModule, CredentialsModule, BoardModule],
    controllers=[UiController],
    providers=[
        ClassProvider(provide=OwnerSessionStore, use_class=MemoryOwnerSessionStore),
        SignInHandler,
        SignOutHandler,
        CurrentSessionHandler,
        ManageSessionsHandler,
        BoardSnapshotHandler,
    ],
)
class UiModule:
    pass
