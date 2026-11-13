"""Gives, keeps, and ends the leases of the sessions.

An app sends the agent key and gets a lease. With no session name, the lease
is on a free pool session of the agent: the server makes the sessions
<agent>.s1 to <agent>.s8 when necessary. When no pool session is free, the
lease starts with no session. The first tool call of that lease then gets a
pool session that is free at that time. With a session name, the lease is on
that named session of the agent. A named session never has a label of the
pool. For each lease on a session, the server gives the session a new random
secret, logs in to the mail server with it, and keeps the secret only in the
memory of the login.
"""

from __future__ import annotations

import datetime as dt

import anyio
from bustan import Injectable

from mai.modules.credentials import (
    AccountStateError,
    AgentKeys,
    CredentialError,
    CredentialService,
    UnknownAccountError,
    check_label,
)
from mai.platform.contract import ToolError

from ..domain.lease import (
    EXPIRES_SECONDS,
    HEARTBEAT_SECONDS,
    MAX_SESSIONS,
    MAX_UNBOUND_LEASES,
    Lease,
    is_pool_label,
    is_token,
    new_token,
    session_login,
    token_hash,
)
from ..domain.lease_store import LeaseStore
from ..domain.new_mail_counter import NewMailCounter
from ..domain.session_state import SessionState
from .log_in_command import LogInCommand, LogInHandler
from .log_out_command import LogOutCommand, LogOutHandler
from .resolve_session_query import ResolveSessionHandler, ResolveSessionQuery

# A lease session has no expiry of its own. The lease gives its life.
SESSION_DAYS = 36500
# The states of a session that can get a lease.
OPEN_STATES = ("issued", "active")
# The tool errors of the mail login that tell that a server part does not reply.
UNAVAILABLE = ("MAIL_SERVER_DOWN", "STORE_UNAVAILABLE")


class LeaseError(Exception):
    """A lease or session operation failed. code is the error code of the API.

    The lease API gives BAD_KEY, BAD_LABEL, NO_FREE_SESSION, LEASE_UNKNOWN,
    SESSION_UNKNOWN, SESSION_REVOKED, or SESSION_BUSY. A failure of the mail
    login gives the code of its tool error, for example MAIL_SERVER_DOWN. The
    owner API of the sessions also gives AGENT_UNKNOWN, SESSION_EXISTS, and
    BAD_REQUEST.

    status is the HTTP status, or None for the usual status of the code.
    retry_safe is None when the error has no such field.
    """

    def __init__(
        self,
        code: str,
        message: str,
        status: int | None = None,
        retry_safe: bool | None = None,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.status = status
        self.retry_safe = retry_safe

    @classmethod
    def of_tool_error(cls, exc: ToolError) -> LeaseError:
        """The lease API error of a tool error. A part that does not reply gives 503."""
        status = 503 if exc.code in UNAVAILABLE else 502
        retry_safe = bool(exc.structured()["error"]["retry_safe"])
        return cls(exc.code, exc.message, status=status, retry_safe=retry_safe)


def _no_free_session(agent: str) -> LeaseError:
    return LeaseError("NO_FREE_SESSION", f"the agent has no free session: {agent}")


@Injectable()
class LeaseService:
    def __init__(
        self,
        store: LeaseStore,
        keys: AgentKeys,
        credentials: CredentialService,
        log_in: LogInHandler,
        resolve: ResolveSessionHandler,
        log_out: LogOutHandler,
        counter: NewMailCounter,
    ) -> None:
        self._store = store
        self._keys = keys
        self._credentials = credentials
        self._log_in = log_in
        self._resolve = resolve
        self._log_out = log_out
        self._counter = counter
        self._lock = anyio.Lock()
        self.now = lambda: dt.datetime.now(dt.UTC)

    async def acquire(
        self,
        agent_key: object,
        app: str,
        folder: str,
        pid: int,
        session: str | None = None,
    ) -> dict:
        """Give a lease to the agent of AGENT_KEY.

        With no SESSION, the lease is on a free pool session. When no pool
        session is free, the lease has no session, and "session" is None.
        SESSION is the label or the login of a named session of the agent, for
        example "lane-a" or "cursor.lane-a".
        """
        agent = await self._agent(agent_key)
        async with self._lock:
            await self._expire()
            try:
                if session is None:
                    login = await self._pool_session(agent)
                else:
                    login = await self._named_session(agent, session)
                handle = None
                if login is not None:
                    handle = (await self._open(login, app)).handle
            except ToolError as exc:
                raise LeaseError.of_tool_error(exc) from None
            token = new_token()
            now = self.now()
            await self._store.put(
                Lease(
                    token_hash=token_hash(token),
                    session=login,
                    agent=agent,
                    app=app,
                    folder=folder,
                    pid=pid,
                    handle=handle,
                    started_at=now,
                    last_seen_at=now,
                )
            )
        return {
            "lease": token,
            "session": login,
            "heartbeat_seconds": HEARTBEAT_SECONDS,
            "expires_seconds": EXPIRES_SECONDS,
        }

    async def heartbeat(self, token: object) -> dict:
        """Keep the lease of TOKEN live. Returns the session and the count of new mail.

        A lease with no session gives the session None and the count 0. When
        the count fails, the count is None.
        """
        lease = await self._live(token)
        lease = await self._store.seen(lease.token_hash, self.now())
        if lease is None:
            raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
        if lease.session is None:
            return {"session": None, "new_mail": 0}
        try:
            state = await self._state(lease)
        except ToolError as exc:
            raise LeaseError.of_tool_error(exc) from None
        return {"session": lease.session, "new_mail": await self._count(state)}

    async def end(self, token: object) -> None:
        """End the lease of TOKEN."""
        lease = await self._live(token)
        await self._finish(lease)

    async def check(self, token: object) -> None:
        """Raise LeaseError(LEASE_UNKNOWN) when TOKEN is not a live lease."""
        await self._live(token)

    async def state(self, token: object) -> SessionState:
        """The session state of the lease of TOKEN, for the MCP tools.

        A lease with no session first gets a free pool session. A failure that
        the tool result tells raises ToolError, for example NO_FREE_SESSION or
        MAIL_SERVER_DOWN. A lease that is not live raises LeaseError.
        """
        lease = await self._live(token)
        if lease.session is None:
            lease = await self._bind(lease)
        return await self._state(lease)

    async def leases(self) -> list[dict]:
        await self._expire()
        return [lease.public() for lease in await self._store.all()]

    async def lease_count(self, agent: str) -> int:
        await self._expire()
        return sum(1 for lease in await self._store.all() if lease.agent == agent)

    async def end_session(self, login: str) -> bool:
        """End the lease of the session LOGIN. Returns False when it has no lease.

        A lease with no session is never the lease of LOGIN. The call waits
        for a lease request or a bind that runs. Thus a lease that starts
        before a rotate or a revoke of the session also ends.
        """
        async with self._lock:
            await self._expire()
            for lease in await self._store.all():
                if lease.session is not None and lease.session == login:
                    await self._finish(lease)
                    return True
        return False

    async def _agent(self, agent_key: object) -> str:
        try:
            return await self._keys.check(agent_key)
        except CredentialError:
            raise LeaseError("BAD_KEY", "the agent key is not valid") from None
        except Exception:  # noqa: BLE001 - a store failure is not a bad key
            raise LeaseError.of_tool_error(ToolError("STORE_UNAVAILABLE")) from None

    async def _bind(self, lease: Lease) -> Lease:
        """Give the lease with no session LEASE a free pool session.

        The bind occurs one time: a call that waits for the lock gets the
        session of the first bind. With no free pool session, the call raises
        ToolError(NO_FREE_SESSION).
        """
        async with self._lock:
            await self._expire()
            current = await self._store.get(lease.token_hash)
            if current is None:
                raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
            if current.session is not None:
                return current
            try:
                login = await self._pool_session(current.agent)
                if login is None:
                    raise _no_free_session(current.agent)
                state = await self._open(login, current.app)
            except LeaseError:
                # The pool session is not free now, also when a revoke occurs
                # during the bind. The next call tries again.
                raise ToolError("NO_FREE_SESSION") from None
            bound = await self._store.bind(current.token_hash, login, state.handle)
            if bound is None:
                # The lease ended during the mail login.
                await self._log_out_handle(state.handle)
                raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
            return bound

    async def _pool_session(self, agent: str) -> str | None:
        """The login of a free pool session of AGENT, or None when no pool session is free.

        None occurs only while the agent has fewer than MAX_UNBOUND_LEASES
        leases with no session. After that limit, the call raises
        LeaseError(NO_FREE_SESSION).
        """
        await self._credential_call(self._credentials.agent_add(agent))
        leases = await self._store.all()
        taken = {lease.session for lease in leases if lease.session is not None}
        rows = await self._credential_call(self._credentials.session_list(agent))
        known = {row["login"]: row for row in rows}
        for number in range(1, MAX_SESSIONS + 1):
            login = session_login(agent, number)
            if login in taken:
                continue
            row = known.get(login)
            if row is None:
                await self._new_pool_session(agent, number)
                return login
            if row.get("status") in OPEN_STATES:
                return login
        unbound = sum(1 for x in leases if x.agent == agent and x.session is None)
        if unbound >= MAX_UNBOUND_LEASES:
            raise _no_free_session(agent)
        return None

    async def _new_pool_session(self, agent: str, number: int) -> None:
        try:
            await self._credentials.session_new(
                agent, f"s{number}", expires_days=SESSION_DAYS
            )
        except AccountStateError:
            # An owner request with a count made the same session at the same time.
            pass
        except UnknownAccountError as exc:
            raise LeaseError("SESSION_UNKNOWN", str(exc)) from None
        except ToolError:
            raise
        except Exception:  # noqa: BLE001 - any failure of the store has one code
            raise ToolError("STORE_UNAVAILABLE") from None

    async def _named_session(self, agent: str, name: str) -> str:
        """The login of the named session NAME of AGENT, when it can get a lease.

        NAME is a label or a login. A login of a different agent gives
        SESSION_UNKNOWN, the same as a session that does not exist. A label
        of the pool gives BAD_LABEL: only a lease with no name gets a pool
        session.
        """
        owner, dot, label = name.partition(".")
        if not dot:
            label = name
        elif owner != agent:
            raise LeaseError("SESSION_UNKNOWN", f"no such session: {name}")
        try:
            check_label(label)
        except ValueError as exc:
            raise LeaseError("BAD_LABEL", str(exc)) from None
        if is_pool_label(label):
            raise LeaseError(
                "BAD_LABEL",
                f"{label} is a label of the pool, not of a named session."
                " A chat that calls no login uses the pool.",
            )
        login = f"{agent}.{label}"
        rows = await self._credential_call(self._credentials.session_list(agent))
        row = next((r for r in rows if r["login"] == login), None)
        if row is None:
            raise LeaseError("SESSION_UNKNOWN", f"no such session: {login}")
        if row.get("status") not in OPEN_STATES:
            raise LeaseError("SESSION_REVOKED", f"the session is closed: {login}")
        if any(lease.session == login for lease in await self._store.all()):
            raise LeaseError("SESSION_BUSY", f"a different lease holds {login}")
        return login

    async def _open(self, login: str, app: str) -> SessionState:
        """Give the session LOGIN a new secret, and log in to the mail server with it.

        A session that a revoke closed gives LeaseError(SESSION_REVOKED). A
        session that does not exist gives LeaseError(SESSION_UNKNOWN). A
        failure of the mail login raises its ToolError.
        """
        try:
            issued = await self._credentials.session_rotate(login)
        except AccountStateError as exc:
            raise LeaseError("SESSION_REVOKED", str(exc)) from None
        except UnknownAccountError as exc:
            raise LeaseError("SESSION_UNKNOWN", str(exc)) from None
        except ToolError:
            raise
        except Exception:  # noqa: BLE001 - any failure of the store has one code
            raise ToolError("STORE_UNAVAILABLE") from None
        return await self._log_in.execute(
            LogInCommand(credential=issued["credential"], client=app or None)
        )

    @staticmethod
    async def _credential_call(awaitable):
        try:
            return await awaitable
        except ToolError:
            raise
        except Exception:  # noqa: BLE001 - any failure of the store has one code
            raise ToolError("STORE_UNAVAILABLE") from None

    async def _count(self, state: SessionState) -> int | None:
        try:
            return await self._counter.count(state)
        except Exception:  # noqa: BLE001 - a failed count must not end the heartbeat
            return None

    async def _live(self, token: object) -> Lease:
        if not is_token(token):
            raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
        lease = await self._store.get(token_hash(str(token)))
        if lease is None:
            raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
        if not lease.is_live(self.now()):
            await self._finish(lease)
            raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
        return lease

    async def _state(self, lease: Lease) -> SessionState:
        """The session state of the lease on a session.

        A part that does not reply raises its ToolError, and the lease stays.
        Each other failure ends the lease and raises LeaseError(LEASE_UNKNOWN).
        """
        try:
            return await self._resolve.execute(ResolveSessionQuery(lease.handle))
        except ToolError as exc:
            if exc.code in UNAVAILABLE:
                raise
            await self._finish(lease)
            raise LeaseError("LEASE_UNKNOWN", "the lease is not known") from None

    async def _finish(self, lease: Lease) -> None:
        # The stored lease can have a session that a bind gave after the read of LEASE.
        removed = await self._store.remove(lease.token_hash)
        if removed is not None and removed.handle is not None:
            await self._log_out_handle(removed.handle)

    async def _log_out_handle(self, handle: str) -> None:
        try:
            await self._log_out.execute(LogOutCommand(handle))
        except ToolError:
            pass

    async def _expire(self) -> None:
        now = self.now()
        for lease in await self._store.all():
            if not lease.is_live(now):
                await self._finish(lease)
