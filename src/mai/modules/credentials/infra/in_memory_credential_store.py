from datetime import datetime

from ..domain.credential import Principal
from ..domain.credential_store import CredentialStore


class InMemoryCredentialStore(CredentialStore):
    """Keeps the accounts in a dict of this process. For tests and the memory backend."""

    def __init__(self) -> None:
        self._principals: dict[str, Principal] = {}
        self._aliases: dict[str, str] = {}
        self._roles: dict[str, dict[str, str]] = {}

    def set_role(self, agent: str, project: str, role: str) -> None:
        self._roles.setdefault(agent, {})[project] = role

    async def get(self, login: str) -> Principal | None:
        return self._principals.get(login)

    async def add(self, principal: Principal) -> bool:
        if principal.login in self._principals:
            return False
        self._principals[principal.login] = principal
        return True

    async def add_alias(self, alias: str, login: str) -> None:
        self._aliases.setdefault(alias, login)

    async def set_hash(self, login: str, password_hash: str, now: datetime) -> None:
        self._update(login, password_hash=password_hash)

    async def set_status(self, login: str, status: str, now: datetime) -> None:
        self._update(login, status=status)

    async def mark_login(self, login: str, now: datetime) -> None:
        self._update(login, status="active", last_login_at=now)

    async def expire_old(self, now: datetime) -> None:
        for p in list(self._principals.values()):
            if (
                p.kind == "session"
                and p.status in ("issued", "active")
                and p.expires_at is not None
                and p.expires_at <= now
            ):
                self._update(p.login, status="expired")

    async def agents(self) -> list[Principal]:
        found = [p for p in self._principals.values() if p.kind == "agent"]
        return sorted(found, key=lambda p: p.login)

    async def sessions(self, agent: str | None) -> list[Principal]:
        found = [
            p
            for p in self._principals.values()
            if p.kind == "session" and (agent is None or p.agent == agent)
        ]
        return sorted(found, key=lambda p: p.login)

    async def roles(self, agent: str) -> dict[str, str]:
        return dict(self._roles.get(agent, {}))

    def _update(self, login: str, **changes) -> None:
        current = self._principals.get(login)
        if current is not None:
            self._principals[login] = current.with_changes(**changes)
