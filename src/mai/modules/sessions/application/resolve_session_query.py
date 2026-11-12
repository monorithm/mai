import datetime as dt
from dataclasses import dataclass

import anyio
from bustan import Injectable

from mai.modules.credentials import CredentialService
from mai.platform.contract import ToolError

from ..domain.handle_store import HandleStore
from ..domain.session_state import LEASE, SessionState, handle_sha
from .session_cache import SessionCache


@dataclass(frozen=True)
class ResolveSessionQuery:
    handle: object


@Injectable()
class ResolveSessionHandler:
    """Returns the live state of a handle and extends its lease, or raises SESSION_UNKNOWN.

    A handle stops when it expires, when the owner revokes the login, or when
    the secret of the login changes.
    """

    def __init__(
        self, credentials: CredentialService, handles: HandleStore, cache: SessionCache
    ) -> None:
        self._credentials = credentials
        self._handles = handles
        self._cache = cache

    async def execute(self, query: ResolveSessionQuery) -> SessionState:
        handle = query.handle
        if not isinstance(handle, str) or not handle.startswith("ses-"):
            raise ToolError("SESSION_UNKNOWN", path="session")
        state = self._cache.get(handle)
        if state is None:
            raise ToolError("SESSION_UNKNOWN", path="session")
        now = dt.datetime.now(dt.UTC)
        expires = now + LEASE
        try:
            login = await self._handles.extend(handle_sha(handle), expires, now)
            live = (
                await self._credentials.login_fingerprint(state.login)
                if login
                else None
            )
        except ToolError:
            raise
        except Exception:  # noqa: BLE001 - any failure maps to one contract code
            raise ToolError("STORE_UNAVAILABLE") from None
        if login != state.login or live is None or live != state.fingerprint:
            await self.forget(handle)
            raise ToolError("SESSION_UNKNOWN", path="session")
        state.expires_at = expires
        return state

    async def forget(self, handle: str) -> None:
        state = self._cache.pop(handle)
        if state is not None:
            await anyio.to_thread.run_sync(_close, state.mail)


def _close(mail: object) -> None:
    # The mail session is any client with a close method; an object without one has nothing to close.
    close = getattr(mail, "close", None)
    if not callable(close):
        return
    try:
        close()
    except Exception:  # noqa: BLE001, S110 - any failure maps to one contract code
        pass
