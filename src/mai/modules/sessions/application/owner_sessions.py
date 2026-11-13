"""The owner operations on the sessions: make, list, rotate, and revoke.

A named session, for example cursor.lane-a, gets a lease only when a bridge
asks for it by name. A pool session has the label s<N>. Only the pool and an
owner request with a count make the sessions with these labels: a named
session never has a label of the pool. No answer holds a session secret: the
server makes each secret and keeps only its hash.
"""

from __future__ import annotations

import logging

from bustan import Injectable

from mai.modules.credentials import (
    AccountStateError,
    CredentialService,
    UnknownAccountError,
    check_label,
)

from ..domain.lease import MAX_SESSIONS, is_pool_label
from .lease_service import OPEN_STATES, SESSION_DAYS, LeaseError, LeaseService

log = logging.getLogger("mai.sessions")


def owner_status(status: object) -> str:
    """The status of a session for the owner: active, expired, or revoked.

    A session that has no login yet is active, because it can get a lease.
    """
    return "active" if status in OPEN_STATES else str(status)


def _summary(row: dict) -> dict:
    return {
        "login": row["login"],
        "agent": row["agent"],
        "label": row["label"],
        "status": owner_status(row.get("status")),
    }


@Injectable()
class OwnerSessions:
    """The sessions of the owner API and of the server CLI."""

    def __init__(self, credentials: CredentialService, leases: LeaseService) -> None:
        self._credentials = credentials
        self._leases = leases

    async def new(self, agent: str, label: object) -> dict:
        """Make the named session AGENT.LABEL. Returns its login, agent, label, and status.

        A label of the pool, for example s1, gives BAD_LABEL.
        """
        try:
            check_label(label)
        except ValueError as exc:
            raise LeaseError("BAD_LABEL", str(exc)) from None
        if is_pool_label(label):
            raise LeaseError(
                "BAD_LABEL",
                f"{label} is a label of the pool, not of a named session."
                " Use --count to make the pool sessions.",
            )
        await self._active_agent(agent)
        return await self._make(agent, str(label))

    async def new_pool(self, agent: str, count: object) -> list[dict]:
        """Make each of the sessions AGENT.s1 to AGENT.sCOUNT that does not exist."""
        if (
            not isinstance(count, int)
            or isinstance(count, bool)
            or not 1 <= count <= MAX_SESSIONS
        ):
            raise LeaseError(
                "BAD_REQUEST", f"count must be an integer from 1 to {MAX_SESSIONS}"
            )
        await self._active_agent(agent)
        known = {row["login"] for row in await self._credentials.session_list(agent)}
        made = []
        for number in range(1, count + 1):
            label = f"s{number}"
            if f"{agent}.{label}" in known:
                continue
            try:
                made.append(await self._make(agent, label))
            except LeaseError as exc:
                # A lease request can make the same pool session at the same time.
                if exc.code != "SESSION_EXISTS":
                    raise
        return made

    async def records(self, agent: str | None = None) -> list[dict]:
        """The sessions and their status, with the time of the last login. No lease data."""
        return [
            dict(_summary(row), last_login_at=row.get("last_login_at"))
            for row in await self._credentials.session_list(agent)
        ]

    async def with_leases(self) -> list[dict]:
        """The sessions, their status, and the app, folder, and process of each live lease."""
        # A lease with no session has no row: this list has one row for each session.
        leases = {
            lease["session"]: lease
            for lease in await self._leases.leases()
            if lease["session"] is not None
        }
        rows = []
        for row in await self._credentials.session_list():
            lease = leases.get(row["login"]) or {}
            rows.append(
                dict(
                    _summary(row),
                    leased=bool(lease),
                    app=lease.get("app"),
                    folder=lease.get("folder"),
                    pid=lease.get("pid"),
                    last_seen_at=lease.get("last_seen_at"),
                )
            )
        return rows

    async def rotate(self, login: str) -> dict:
        """Give the session LOGIN a new internal secret, and end its lease."""
        row = await self._session(login)
        if owner_status(row.get("status")) != "active":
            raise LeaseError("SESSION_REVOKED", f"the session is closed: {login}")
        try:
            # The answer holds the new credential. It stays in this function.
            await self._credentials.session_rotate(login)
        except UnknownAccountError as exc:
            raise LeaseError("SESSION_UNKNOWN", str(exc)) from None
        except AccountStateError as exc:
            raise LeaseError("SESSION_REVOKED", str(exc)) from None
        await self._leases.end_session(login)
        return {"login": login, "status": "active"}

    async def revoke(self, login: str) -> dict:
        """Close the session LOGIN for good, and end its lease."""
        row = await self._session(login)
        if row.get("status") != "revoked":
            await self._credentials.session_revoke(login)
        await self._leases.end_session(login)
        return {"login": login, "status": "revoked"}

    async def end_lease(self, login: str) -> bool:
        """End the lease of the session LOGIN. Returns False when it has no lease."""
        return await self._leases.end_session(login)

    async def _active_agent(self, agent: str) -> None:
        if agent not in await self._credentials.agent_names():
            raise LeaseError("AGENT_UNKNOWN", f"no such agent: {agent}")

    async def _session(self, login: str) -> dict:
        agent, dot, _label = login.partition(".")
        rows = await self._credentials.session_list(agent) if dot else []
        row = next((r for r in rows if r["login"] == login), None)
        if row is None:
            raise LeaseError("SESSION_UNKNOWN", f"no such session: {login}")
        return row

    async def _make(self, agent: str, label: str) -> dict:
        try:
            made = await self._credentials.session_new(
                agent, label, expires_days=SESSION_DAYS
            )
        except AccountStateError as exc:
            raise LeaseError("SESSION_EXISTS", str(exc)) from None
        except UnknownAccountError as exc:
            raise LeaseError("AGENT_UNKNOWN", str(exc)) from None
        except ValueError as exc:
            raise LeaseError("BAD_LABEL", str(exc)) from None
        login = made["login"]
        if not made.get("acl_set"):
            log.warning("the queue rights of %s are not set", login)
        return {"login": login, "agent": agent, "label": label, "status": "active"}
