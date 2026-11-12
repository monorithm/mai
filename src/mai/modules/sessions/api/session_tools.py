from bustan import Injectable

from mai.platform.contract import Ctx, ToolError

from ..application.session_service import SessionService
from ..domain.session_state import iso


def _server_version() -> str:
    try:
        from importlib.metadata import version

        return version("mai")
    except Exception:  # noqa: BLE001 - any failure maps to one contract code
        from mai import __version__

        return __version__


@Injectable()
class SessionTools:
    """The MCP tools login, logout, and whoami."""

    def __init__(self, sessions: SessionService) -> None:
        self._sessions = sessions

    def tools(self) -> dict:
        return {"logout": self.logout, "whoami": self.whoami}

    async def login(self, ctx: Ctx, args: dict) -> dict:
        client = args.get("client")
        if client is not None and not isinstance(client, str):
            raise ToolError("INVALID_ARGUMENT", path="client")
        result = await self._sessions.login(
            credential=args.get("credential"),
            credential_file=args.get("credential_file"),
            client=client,
        )
        if getattr(ctx, "transport", None) == "stdio":
            # The stdio process keeps the handle, so later calls need no "session" argument.
            ctx.session_state = await self._sessions.resolve(result["session"])
        return result

    async def logout(self, ctx: Ctx, args: dict) -> dict:
        state = await self._sessions.state_for(ctx, args)
        await self._sessions.logout(state.handle)
        current = getattr(ctx, "session_state", None)
        if current is not None and current.handle == state.handle:
            ctx.session_state = None
        return {"ended": True}

    async def whoami(self, ctx: Ctx, args: dict) -> dict:
        try:
            state = await self._sessions.state_for(ctx, args)
        except ToolError as exc:
            if exc.code in ("NOT_LOGGED_IN", "SESSION_UNKNOWN") and not args.get(
                "session"
            ):
                return {
                    "logged_in": False,
                    "login": None,
                    "agent": None,
                    "roles": {},
                    "expires_at": None,
                    "server_version": _server_version(),
                }
            raise
        return {
            "logged_in": True,
            "login": state.login,
            "agent": state.agent,
            "roles": dict(state.roles),
            "expires_at": iso(state.expires_at),
            "server_version": _server_version(),
        }
