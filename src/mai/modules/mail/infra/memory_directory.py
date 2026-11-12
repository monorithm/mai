"""The memory adapter of the directory, for the memory backend and the tests."""

from __future__ import annotations

from bustan import Injectable

from ..domain.ports import Directory, SessionEntry


@Injectable()
class InMemoryDirectory(Directory):
    """Agents and live sessions that a test or the memory backend adds."""

    def __init__(self) -> None:
        self._agents: dict[str, list[SessionEntry]] = {}

    def add_agent(self, agent: str) -> None:
        self._agents.setdefault(agent, [])

    def add_session(
        self, agent: str, login: str, label: str | None = None, last_login_at=None
    ) -> None:
        self._agents.setdefault(agent, []).append(
            SessionEntry(login, label, last_login_at)
        )

    async def known_logins(self, logins):
        live = set(self._agents)
        for entries in self._agents.values():
            live.update(e.login for e in entries)
        return {login for login in logins if login in live}

    async def agents(self):
        return {
            a: sorted(e, key=lambda s: s.login) for a, e in sorted(self._agents.items())
        }
