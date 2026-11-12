import datetime as dt
from dataclasses import dataclass

from bustan import Injectable

from mai.platform.contract import ToolError

from ..domain.handle_store import HandleStore
from ..domain.session_state import handle_sha
from .resolve_session_query import ResolveSessionHandler, ResolveSessionQuery


@dataclass(frozen=True)
class LogOutCommand:
    handle: object


@Injectable()
class LogOutHandler:
    """Ends a handle. An unknown handle gets SESSION_UNKNOWN."""

    def __init__(self, resolve: ResolveSessionHandler, handles: HandleStore) -> None:
        self._resolve = resolve
        self._handles = handles

    async def execute(self, command: LogOutCommand) -> None:
        state = await self._resolve.execute(ResolveSessionQuery(command.handle))
        try:
            await self._handles.end(handle_sha(state.handle), dt.datetime.now(dt.UTC))
        except Exception:  # noqa: BLE001 - any failure maps to one contract code
            raise ToolError("STORE_UNAVAILABLE") from None
        await self._resolve.forget(state.handle)
