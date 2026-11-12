import os

from bustan import Injectable

from mai.platform.contract import ToolError

from ..domain.session_state import SessionState, login_result
from .log_in_command import LogInCommand, LogInHandler
from .log_out_command import LogOutCommand, LogOutHandler
from .resolve_session_query import ResolveSessionHandler, ResolveSessionQuery

CREDENTIAL_ENV = "MAI_CREDENTIAL_FILE"


@Injectable()
class SessionService:
    """The exported face of the sessions module."""

    def __init__(
        self,
        log_in: LogInHandler,
        resolve: ResolveSessionHandler,
        log_out: LogOutHandler,
    ) -> None:
        self._log_in = log_in
        self._resolve = resolve
        self._log_out = log_out

    async def login(self, credential=None, credential_file=None, client=None) -> dict:
        """Log in with a credential string or a credential file. Returns the result of the login tool."""
        state = await self.login_state(credential, credential_file, client)
        return login_result(state)

    async def login_state(
        self, credential=None, credential_file=None, client=None
    ) -> SessionState:
        return await self._log_in.execute(
            LogInCommand(
                credential=credential, credential_file=credential_file, client=client
            )
        )

    async def resolve(self, handle) -> SessionState:
        return await self._resolve.execute(ResolveSessionQuery(handle))

    async def logout(self, handle) -> None:
        await self._log_out.execute(LogOutCommand(handle))

    async def auto_login(self, client=None) -> SessionState | None:
        """Log in from MAI_CREDENTIAL_FILE when it is set. The stdio server calls this at start."""
        path = os.environ.get(CREDENTIAL_ENV)
        if not path:
            return None
        return await self.login_state(credential_file=path, client=client)

    async def state_for(self, ctx, args: dict) -> SessionState:
        """The session of a tool call: the argument "session" first, then the stdio state of ctx."""
        handle = args.get("session") if isinstance(args, dict) else None
        if handle:
            return await self.resolve(handle)
        state = getattr(ctx, "session_state", None)
        if state is not None:
            return await self.resolve(state.handle)
        if getattr(ctx, "transport", None) == "stdio":
            raise ToolError("NOT_LOGGED_IN")
        raise ToolError("SESSION_UNKNOWN", path="session")
