import datetime as dt
import inspect
from dataclasses import dataclass

import anyio
from bustan import Injectable

from mai.modules.credentials import CredentialService
from mai.platform.contract import ToolError
from mai.platform.mailserver import MailClientFactory

from ..domain.handle_store import HandleStore
from ..domain.session_state import LEASE, SessionState, handle_sha, new_handle
from .session_cache import SessionCache


@dataclass(frozen=True)
class LogInCommand:
    credential: str | None = None
    credential_file: str | None = None
    client: str | None = None


@Injectable()
class LogInHandler:
    """Checks the credential, opens the IMAP and SMTP connections of the login,
    and stores a new handle with a lease of 12 hours."""

    def __init__(
        self,
        credentials: CredentialService,
        handles: HandleStore,
        mail: MailClientFactory,
        cache: SessionCache,
    ) -> None:
        self._credentials = credentials
        self._handles = handles
        self._mail = mail
        self._cache = cache

    async def execute(self, command: LogInCommand) -> SessionState:
        if bool(command.credential) == bool(command.credential_file):
            raise ToolError("INVALID_ARGUMENT", path="credential")
        credential = command.credential
        if command.credential_file:
            credential = self._credentials.read_credential_file(command.credential_file)
        login, secret = self._credentials.parse_credential(credential)
        principal = await self._store_call(
            self._credentials.authenticate(login, secret)
        )
        agent = principal.get("agent") or principal.get("login") or login
        kind = principal.get("kind", "session")
        roles = await self._store_call(self._credentials.roles(agent))
        mail = await self._connect(login, secret)
        handle = new_handle()
        expires = dt.datetime.now(dt.UTC) + LEASE
        try:
            await self._store_call(
                self._handles.add(handle_sha(handle), login, expires)
            )
        except ToolError:
            await anyio.to_thread.run_sync(_close, mail)
            raise
        state = SessionState(
            login=login,
            agent=agent,
            roles=roles,
            handle=handle,
            mail=mail,
            expires_at=expires,
            kind=kind,
            client=command.client,
            credential_expires_at=principal.get("expires_at_value"),
            fingerprint=principal.get("fingerprint"),
        )
        self._cache.put(state)
        return state

    async def _connect(self, login: str, secret: str) -> object:
        session = self._mail.open(login, secret)
        if inspect.isawaitable(session):
            session = await session
        try:
            await anyio.to_thread.run_sync(session.connect)
        except ToolError:
            raise
        except Exception as exc:  # noqa: BLE001 - the mail library raises many kinds
            text = str(exc).lower()
            if "auth" in text or "login" in text or "credential" in text:
                raise ToolError("LOGIN_FAILED", path="credential") from None
            raise ToolError("MAIL_SERVER_DOWN") from None
        return session

    @staticmethod
    async def _store_call(awaitable):
        try:
            return await awaitable
        except ToolError:
            raise
        except Exception:  # noqa: BLE001 - any failure maps to one contract code
            raise ToolError("STORE_UNAVAILABLE") from None


def _close(mail: object) -> None:
    # The mail session is any client with a close method; an object without one has nothing to close.
    close = getattr(mail, "close", None)
    if not callable(close):
        return
    try:
        close()
    except Exception:  # noqa: BLE001, S110 - any failure maps to one contract code
        pass
