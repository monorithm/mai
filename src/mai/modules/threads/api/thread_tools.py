"""The MCP tools claim_thread and release_thread."""

from __future__ import annotations

from bustan import Injectable

from mai.modules.sessions import SessionService
from mai.platform.contract import Ctx, ToolError

from ..application.claim_service import ClaimService


@Injectable()
class ThreadTools:
    def __init__(self, claims: ClaimService, sessions: SessionService) -> None:
        self._claims = claims
        self._sessions = sessions

    def tools(self) -> dict:
        return {
            "claim_thread": self.claim_thread,
            "release_thread": self.release_thread,
        }

    async def _state(self, ctx: Ctx, args: dict):
        """The session of a call: the argument "session" first, then the stdio state of ctx."""
        handle = args.get("session")
        if handle:
            return await self._sessions.resolve(handle)
        if ctx.session_state is not None:
            return await self._sessions.resolve(ctx.session_state.handle)
        if ctx.transport == "stdio":
            raise ToolError("NOT_LOGGED_IN")
        raise ToolError("SESSION_UNKNOWN", path="session")

    async def claim_thread(self, ctx: Ctx, args: dict) -> dict:
        state = await self._state(ctx, args)
        return await self._claims.claim(
            state, ref=args.get("ref"), thread_ref=args.get("thread_ref")
        )

    async def release_thread(self, ctx: Ctx, args: dict) -> dict:
        state = await self._state(ctx, args)
        if args.get("ref") is not None:
            raise ToolError("INVALID_ARGUMENT", path="ref")
        return await self._claims.release(state, args.get("thread_ref"))
