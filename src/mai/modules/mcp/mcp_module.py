from bustan import FactoryProvider, Module, ValueProvider

from mai.modules.board import BoardModule, BoardTools
from mai.modules.credentials import CredentialsModule
from mai.modules.mail import MailModule, MailTools
from mai.modules.sessions import (
    LeaseService,
    SessionService,
    SessionsModule,
    SessionTools,
)
from mai.modules.threads import ThreadsModule, ThreadTools
from mai.platform.health import HealthChecksModule

from .api.mcp_controller import McpController
from .api.mcp_http_policy import McpHttpPolicy
from .api.server_controller import ServerController
from .application.dispatcher import Dispatcher
from .application.wait_lane import WaitLane


def _dispatcher(
    sessions: SessionService,
    session_tools: SessionTools,
    mail_tools: MailTools,
    thread_tools: ThreadTools,
    board_tools: BoardTools,
    leases: LeaseService,
) -> Dispatcher:
    registry: dict = {}
    for provider in (session_tools, mail_tools, thread_tools, board_tools):
        registry.update(provider.tools())
    return Dispatcher(registry, sessions.resolve, leases.state)


# The MCP module owns the JSON-RPC dispatcher and its two transports. It has no tools of its own: the
# factory merges the tool maps of the sessions, mail, threads, and board modules into one registry,
# and gives the dispatcher the resolver of the sessions module. The stdio entry point gets the same
# Dispatcher from the application context. The policy reads its allowlists from the environment once.
@Module(
    imports=[
        SessionsModule,
        MailModule,
        ThreadsModule,
        BoardModule,
        CredentialsModule,
        HealthChecksModule,
    ],
    controllers=[McpController, ServerController],
    providers=[
        FactoryProvider(
            provide=Dispatcher,
            use_factory=_dispatcher,
            inject=(
                SessionService,
                SessionTools,
                MailTools,
                ThreadTools,
                BoardTools,
                LeaseService,
            ),
        ),
        FactoryProvider(
            provide=McpHttpPolicy, use_factory=McpHttpPolicy.from_environment, inject=()
        ),
        ValueProvider(provide=WaitLane, use_value=WaitLane()),
    ],
    exports=[Dispatcher],
)
class McpModule:
    pass
