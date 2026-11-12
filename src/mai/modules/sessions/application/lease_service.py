"""Gives, keeps, and ends the leases of the sessions.

An app sends the agent key and gets a lease on a free session of the agent.
The server makes the sessions <agent>.s1 to <agent>.s8 when necessary. For
each lease, the server gives the session a new random secret, logs in to the
mail server with it, and keeps the secret only in the memory of the login.
"""

from __future__ import annotations

import datetime as dt

import anyio
from bustan import Injectable

from mai.modules.credentials import AgentKeys, CredentialService
from mai.platform.contract import ToolError

from ..domain.lease import (
    EXPIRES_SECONDS,
    HEARTBEAT_SECONDS,
    MAX_SESSIONS,
    Lease,
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
_SESSION_DAYS = 36500


class LeaseError(Exception):
    """A lease operation failed. code is BAD_KEY, NO_FREE_SESSION, or LEASE_UNKNOWN."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


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

    async def acquire(self, agent_key: object, app: str, folder: str, pid: int) -> dict:
        """Give a lease on a free session of the agent of AGENT_KEY."""
        try:
            agent = await self._keys.check(agent_key)
        except Exception:  # noqa: BLE001 - each failure of the key check is BAD_KEY
            raise LeaseError("BAD_KEY", "the agent key is not valid") from None
        async with self._lock:
            await self._expire()
            login = await self._free_session(agent)
            issued = await self._credentials.session_rotate(login)
            state = await self._log_in.execute(
                LogInCommand(credential=issued["credential"], client=app or None)
            )
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
                    handle=state.handle,
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
        """Keep the lease of TOKEN live. Returns the session and the count of new mail."""
        lease = await self._live(token)
        await self._store.put(lease.seen(self.now()))
        state = await self._state(lease)
        return {"session": lease.session, "new_mail": await self._counter.count(state)}

    async def end(self, token: object) -> None:
        """End the lease of TOKEN."""
        lease = await self._live(token)
        await self._finish(lease)

    async def state(self, token: object) -> SessionState:
        """The session state of the lease of TOKEN, for the MCP tools."""
        lease = await self._live(token)
        return await self._state(lease)

    async def leases(self) -> list[dict]:
        await self._expire()
        return [lease.public() for lease in await self._store.all()]

    async def sessions(self) -> list[dict]:
        """The sessions of the active agents, and if each has a live lease."""
        await self._expire()
        leased = {lease.session for lease in await self._store.all()}
        rows = []
        for row in await self._credentials.session_list():
            if row.get("status") not in ("issued", "active"):
                continue
            rows.append(
                {
                    "login": row["login"],
                    "agent": row["agent"],
                    "leased": row["login"] in leased,
                }
            )
        return rows

    async def lease_count(self, agent: str) -> int:
        await self._expire()
        return sum(1 for lease in await self._store.all() if lease.agent == agent)

    async def end_session(self, login: str) -> bool:
        """End the lease of the session LOGIN. Returns False when it has no lease."""
        await self._expire()
        for lease in await self._store.all():
            if lease.session == login:
                await self._finish(lease)
                return True
        return False

    async def _free_session(self, agent: str) -> str:
        await self._credentials.agent_add(agent)
        taken = {lease.session for lease in await self._store.all()}
        known = {
            row["login"]: row for row in await self._credentials.session_list(agent)
        }
        for number in range(1, MAX_SESSIONS + 1):
            login = session_login(agent, number)
            if login in taken:
                continue
            row = known.get(login)
            if row is None:
                await self._credentials.session_new(
                    agent, f"s{number}", expires_days=_SESSION_DAYS
                )
                return login
            if row.get("status") in ("issued", "active"):
                return login
        raise LeaseError("NO_FREE_SESSION", "the agent has no free session")

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
        try:
            return await self._resolve.execute(ResolveSessionQuery(lease.handle))
        except ToolError:
            await self._finish(lease)
            raise LeaseError("LEASE_UNKNOWN", "the lease is not known") from None

    async def _finish(self, lease: Lease) -> None:
        await self._store.remove(lease.token_hash)
        try:
            await self._log_out.execute(LogOutCommand(lease.handle))
        except ToolError:
            pass

    async def _expire(self) -> None:
        now = self.now()
        for lease in await self._store.all():
            if not lease.is_live(now):
                await self._finish(lease)
