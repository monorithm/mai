"""The board tools of the MCP contract: one handler for each tool, on top of the board store."""

from functools import partial

from mai.modules.sessions import SessionService
from mai.platform.contract import ToolError

from ..domain.board_store import BoardStore

TOOL_NAMES = (
    "get_board",
    "read_item",
    "start_item",
    "report_status",
    "submit_for_review",
    "plan_item",
    "assign_item",
    "review_item",
    "manage_sprint",
)


class BoardTools:
    def __init__(self, store: BoardStore, sessions: SessionService):
        self._store = store
        self._sessions = sessions

    async def _state(self, ctx, args):
        """The session of the call: the argument "session" first, then the stdio state of ctx."""
        handle = args.get("session") if isinstance(args, dict) else None
        if handle:
            return await self._sessions.resolve(handle)
        state = getattr(ctx, "session_state", None)
        if state is not None:
            return await self._sessions.resolve(state.handle)
        if getattr(ctx, "transport", None) == "stdio":
            raise ToolError("NOT_LOGGED_IN")
        raise ToolError("SESSION_UNKNOWN", path="session")

    async def _handle(self, tool, ctx, args):
        if not isinstance(args, dict):
            raise ToolError("INVALID_ARGUMENT")
        state = await self._state(ctx, args)
        return await self._store.call(tool, ctx, args, state)

    def tools(self):
        return {name: partial(self._handle, name) for name in TOOL_NAMES}
