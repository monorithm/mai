"""Gives, keeps, and ends the leases of the sessions.

An app sends the agent key and gets a lease. With no session name, the lease
is on a free pool session of the agent: the server makes the pool sessions
when necessary, up to 8 open pool sessions. A revoked pool session does not
hold a place in the pool. When no pool session is free, the lease starts with
no session. The first tool call of that lease then gets a
pool session that is free at that time. A lease with no session ends when the
owner removes its agent. With a session name, the lease is on that named
session of the agent. A named session never has a label of the pool. For each
lease on a session, the server gives the session a new random secret, logs in
to the mail server with it, and keeps the secret only in the memory of the
login. Each lease operation has a time limit, thus each failure has a code.
"""

from __future__ import annotations

import datetime as dt
import itertools

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
from mai.platform.contract.time_limit import time_limit, time_limit_part

from ..domain.lease import (
    EXPIRES_SECONDS,
    HEARTBEAT_SECONDS,
    MAX_SESSIONS,
    MAX_UNBOUND_LEASES,
    Lease,
    is_pool_label,
    is_token,
    new_token,
    pool_number,
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
# The tool errors that tell that a server part does not reply, or that the
# server is busy. The lease API gives them with 503, and a lease stays.
UNAVAILABLE = ("MAIL_SERVER_DOWN", "STORE_UNAVAILABLE", "BUSY")
# The time limits of the lease operations, in seconds. Each limit is less than
# the request limit of the HTTP server (75 s). The limit of a lease request is
# less than the wait of the bridge for a lease request (30 s). The limit of a
# heartbeat is less than the wait of the bridge for a heartbeat (15 s). Thus
# each failure gets a coded answer, and the bridge gets the answer.
REQUEST_LIMIT_SECONDS = 20.0
HEARTBEAT_LIMIT_SECONDS = 10.0
# The part of the heartbeat limit for the count of new mail.
COUNT_LIMIT_SECONDS = 5.0
# The time limit of a logout that ends a lease. The logout does not stop the
# end of the lease.
LOGOUT_LIMIT_SECONDS = 3.0


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


class _AgentRemoved(Exception):
    """The owner removed the agent during a lease operation."""


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
        self.request_seconds = REQUEST_LIMIT_SECONDS
        self.heartbeat_seconds = HEARTBEAT_LIMIT_SECONDS
        self.count_seconds = COUNT_LIMIT_SECONDS

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
        example "lane-a" or "cursor.lane-a". The request has a time limit:
        then the error is MAIL_SERVER_DOWN, STORE_UNAVAILABLE, or BUSY.
        """
        try:
            with time_limit(self.request_seconds):
                token, login = await self._acquire(agent_key, app, folder, pid, session)
        except ToolError as exc:
            raise LeaseError.of_tool_error(exc) from None
        return {
            "lease": token,
            "session": login,
            "heartbeat_seconds": HEARTBEAT_SECONDS,
            "expires_seconds": EXPIRES_SECONDS,
        }

    async def _acquire(
        self, agent_key: object, app: str, folder: str, pid: int, session: str | None
    ) -> tuple[str, str | None]:
        """The token and the session of a new lease. A failure raises LeaseError or ToolError."""
        agent = await self._agent(agent_key)
        with time_limit_part("BUSY"):
            await self._lock.acquire()
        try:
            await self._expire()
            # The owner can remove the agent while the request waits for the lock.
            if not await self._credential_call(self._keys.is_active(agent)):
                raise LeaseError("BAD_KEY", "the agent key is not valid")
            login, handle = None, None
            if session is None:
                try:
                    opened = await self._open_pool_session(agent, app)
                except _AgentRemoved:
                    raise LeaseError("BAD_KEY", "the agent key is not valid") from None
                if opened is None:
                    await self._check_unbound_limit(agent)
                else:
                    login, handle = opened[0], opened[1].handle
            else:
                login = await self._named_session(agent, session)
                handle = (await self._open(login, app)).handle
            token = new_token()
            now = self.now()
            lease = Lease(
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
            try:
                await self._store.put(lease)
            except BaseException:
                # The lease does not start. Its mail login must also end.
                if handle is not None:
                    await self._log_out_handle(handle)
                raise
        finally:
            self._lock.release()
        return token, login

    async def heartbeat(self, token: object) -> dict:
        """Keep the lease of TOKEN live. Returns the session and the count of new mail.

        A lease with no session gives the session None and the count 0. When
        the count fails or takes too long, the count is None. A lease with no
        session ends when the owner removes its agent. The heartbeat has a
        time limit: then the error is STORE_UNAVAILABLE or MAIL_SERVER_DOWN,
        and the lease stays.
        """
        try:
            with time_limit(self.heartbeat_seconds):
                lease = await self._live(token)
                lease = await self._store.seen(lease.token_hash, self.now())
                if lease is None:
                    raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
                await self._check_agent(lease)
                if lease.session is None:
                    return {"session": None, "new_mail": 0}
                state = await self._state(lease)
                return {"session": lease.session, "new_mail": await self._count(state)}
        except ToolError as exc:
            raise LeaseError.of_tool_error(exc) from None

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
        MAIL_SERVER_DOWN. A lease that is not live raises LeaseError. The call
        has the time limit of a lease request.
        """
        with time_limit(self.request_seconds):
            lease = await self._live(token)
            if lease.session is None:
                lease = await self._bind(lease)
            else:
                await self._check_agent(lease)
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
        ToolError(NO_FREE_SESSION). When the owner removed the agent, the
        lease ends and the call raises LeaseError(LEASE_UNKNOWN).
        """
        with time_limit_part("BUSY"):
            await self._lock.acquire()
        try:
            await self._expire()
            current = await self._store.get(lease.token_hash)
            if current is None:
                raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
            if current.session is not None:
                return current
            await self._check_agent(current)
            try:
                opened = await self._open_pool_session(current.agent, current.app)
            except _AgentRemoved:
                await self._finish(current)
                raise LeaseError("LEASE_UNKNOWN", "the lease is not known") from None
            except LeaseError:
                # The selected pool session cannot get a lease now. The next
                # call tries again.
                raise ToolError("NO_FREE_SESSION") from None
            if opened is None:
                raise ToolError("NO_FREE_SESSION")
            login, state = opened
            try:
                bound = await self._store.bind(current.token_hash, login, state.handle)
            except BaseException:
                # The bind does not occur. Its mail login must also end.
                await self._log_out_handle(state.handle)
                raise
            if bound is None:
                # The lease ended during the mail login.
                await self._log_out_handle(state.handle)
                raise LeaseError("LEASE_UNKNOWN", "the lease is not known")
            return bound
        finally:
            self._lock.release()

    async def _check_agent(self, lease: Lease) -> None:
        """End LEASE and raise LeaseError(LEASE_UNKNOWN) when the owner removed its agent.

        The removal of an agent revokes its sessions, but it does not wait for
        the lease lock. A lease request or a bind at the same time can thus
        give a lease on a session that the removal did not see. This check
        ends such a lease, and revokes its session. A lease with no session
        has no session to revoke, thus it also needs this check.
        """
        if await self._credential_call(self._keys.is_active(lease.agent)):
            return
        await self._finish(lease)
        if lease.session is not None:
            with anyio.move_on_after(LOGOUT_LIMIT_SECONDS, shield=True):
                try:
                    await self._credentials.session_revoke(lease.session)
                except Exception:  # noqa: BLE001, S110 - the lease ended, the revoke only cleans up
                    pass
        raise LeaseError("LEASE_UNKNOWN", "the lease is not known")

    async def _check_unbound_limit(self, agent: str) -> None:
        """Raise LeaseError(NO_FREE_SESSION) when AGENT has its limit of leases with no session."""
        unbound = sum(
            1
            for lease in await self._store.all()
            if lease.agent == agent and lease.session is None
        )
        if unbound >= MAX_UNBOUND_LEASES:
            raise _no_free_session(agent)

    async def _open_pool_session(
        self, agent: str, app: str
    ) -> tuple[str, SessionState] | None:
        """Give a free pool session of AGENT a new secret and a mail login.

        Returns the login and the session state, or None when no pool session
        is free. A revoke can close the selected session after the read of the
        session list. Then the call tries the next free pool session. When the
        session list tells that the selected session can get a lease, the
        LeaseError of the rotate goes to the caller. A removed agent raises
        _AgentRemoved.
        """
        tried: set[str] = set()
        login_failed = False
        while True:
            login = await self._pool_session(agent, tried)
            if login is None:
                return None
            try:
                return login, await self._open(login, app)
            except LeaseError:
                if await self._is_open(agent, login):
                    raise
                tried.add(login)
            except ToolError as exc:
                # An owner rotate of the selected session after its new secret
                # makes the mail login fail. The call tries one different
                # session. A second failure tells a different cause.
                if exc.code != "LOGIN_FAILED" or login_failed:
                    raise
                login_failed = True
                tried.add(login)

    async def _pool_session(self, agent: str, tried: set[str]) -> str | None:
        """The login of a free pool session of AGENT that is not in TRIED, or None.

        A revoke is final, thus a closed pool session does not hold a place in
        the pool. The pool has a maximum of MAX_SESSIONS open pool sessions. A
        free open pool session comes first, in the order of its number. Else,
        when the pool has less than MAX_SESSIONS open pool sessions, the call
        makes the pool session with the lowest number that is not in use.
        """
        taken = {
            lease.session
            for lease in await self._store.all()
            if lease.session is not None
        }
        rows = await self._credential_call(self._credentials.session_list(agent))
        used: set[int] = set()
        open_pool: list[tuple[int, str]] = []
        for row in rows:
            number = pool_number(row.get("label"))
            if number is None:
                continue
            used.add(number)
            if row.get("status") in OPEN_STATES:
                open_pool.append((number, row["login"]))
        for _number, login in sorted(open_pool):
            if login not in taken and login not in tried:
                return login
        if len(open_pool) >= MAX_SESSIONS:
            return None
        number = next(n for n in itertools.count(1) if n not in used)
        login = session_login(agent, number)
        if login in tried:
            return None
        # A removal of the agent revokes its sessions, thus the pool has free
        # places. A removed agent must not get a new pool session.
        if not await self._credential_call(self._keys.is_active(agent)):
            raise _AgentRemoved
        await self._new_pool_session(agent, number)
        return login

    async def _is_open(self, agent: str, login: str) -> bool:
        """True when the session list of AGENT tells that LOGIN can get a lease."""
        rows = await self._credential_call(self._credentials.session_list(agent))
        return any(
            row["login"] == login and row.get("status") in OPEN_STATES for row in rows
        )

    async def _new_pool_session(self, agent: str, number: int) -> None:
        """Make the pool session sNUMBER of AGENT.

        A removed agent raises _AgentRemoved: the credentials refuse a new
        session of an agent that is not active.
        """
        try:
            await self._credentials.agent_add(agent)
            await self._credentials.session_new(
                agent, f"s{number}", expires_days=SESSION_DAYS
            )
        except AccountStateError:
            # An owner request with a count made the same session at the same time.
            pass
        except UnknownAccountError:
            raise _AgentRemoved from None
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
                " A tool call with no session argument uses the pool.",
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

        A session that a revoke closed gives LeaseError(SESSION_REVOKED), also
        when the revoke comes after the new secret: then the check of the
        secret gives CREDENTIAL_REVOKED, or the mail server refuses the login
        with LOGIN_FAILED. A session that does not exist gives
        LeaseError(SESSION_UNKNOWN). Each other failure of the mail login
        raises its ToolError.
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
        try:
            return await self._log_in.execute(
                LogInCommand(credential=issued["credential"], client=app or None)
            )
        except ToolError as exc:
            revoked = exc.code == "CREDENTIAL_REVOKED" or (
                exc.code == "LOGIN_FAILED"
                and not await self._is_open(login.partition(".")[0], login)
            )
            if revoked:
                raise LeaseError(
                    "SESSION_REVOKED", f"the session is closed: {login}"
                ) from None
            raise

    @staticmethod
    async def _credential_call(awaitable):
        try:
            return await awaitable
        except ToolError:
            raise
        except Exception:  # noqa: BLE001 - any failure of the store has one code
            raise ToolError("STORE_UNAVAILABLE") from None

    async def _count(self, state: SessionState) -> int | None:
        """The count of new mail of STATE, or None when it fails or takes too long."""
        try:
            with anyio.fail_after(self.count_seconds):
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
        """End the mail login of HANDLE, and close its mail connections.

        The logout runs also when the time limit of the operation occurred.
        A failure or its own time limit stops only the logout. The mail
        connections close also when the store did not end the handle.
        """
        with anyio.move_on_after(LOGOUT_LIMIT_SECONDS, shield=True):
            try:
                await self._log_out.execute(LogOutCommand(handle))
            except ToolError:
                pass
        with anyio.CancelScope(shield=True):
            # The close has its own time limit.
            await self._resolve.forget(handle)

    async def _log_out_handles(self, handles: list[str]) -> None:
        """End the mail logins of HANDLES at the same time.

        Thus the wait is the wait of one logout, also for many handles.
        """
        if not handles:
            return
        with anyio.CancelScope(shield=True):
            async with anyio.create_task_group() as group:
                for handle in handles:
                    group.start_soon(self._log_out_handle, handle)

    async def _expire(self) -> None:
        """End each lease that is not live. The logouts occur at the same time."""
        now = self.now()
        handles: list[str] = []
        for lease in await self._store.all():
            if lease.is_live(now):
                continue
            removed = await self._store.remove(lease.token_hash)
            if removed is not None and removed.handle is not None:
                handles.append(removed.handle)
        await self._log_out_handles(handles)
