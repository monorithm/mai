"""Gives, rotates, checks, and removes the keys of the agents.

An agent key is the string "mai1:<agent>:<secret>". The server shows the key
one time, in the answer that makes it. The store keeps only a hash.
"""

from __future__ import annotations

from datetime import UTC, datetime

from bustan import Injectable

from ..domain.agent_key_store import AgentKeyStore
from ..domain.credential import (
    Principal,
    check_agent_name,
    format_credential,
    new_secret,
    parse_credential,
)
from ..domain.credential_store import CredentialStore
from ..domain.errors import AccountStateError, CredentialError, UnknownAccountError
from ..domain.secret_hasher import SecretHasher
from .add_agent_command import AddAgentCommand, AddAgentHandler


@Injectable()
class AgentKeys:
    """The agent key operations of the owner API and of the lease API."""

    def __init__(
        self,
        store: CredentialStore,
        keys: AgentKeyStore,
        hasher: SecretHasher,
        add_agent: AddAgentHandler,
    ) -> None:
        self._store = store
        self._keys = keys
        self._hasher = hasher
        self._add_agent = add_agent

    async def add(self, name: str) -> str:
        """Make the agent NAME and its first key. Returns the key.

        Raises ValueError for a bad name and AccountStateError when the agent
        has a key.
        """
        agent = check_agent_name(name)
        current = await self._store.get(agent)
        if (
            current is not None
            and current.kind == "agent"
            and current.status == "active"
            and await self._keys.get_hash(agent)
        ):
            raise AccountStateError(f"agent exists: {agent}")
        await self._add_agent.execute(AddAgentCommand(name=agent))
        now = datetime.now(UTC)
        await self._store.set_status(agent, "active", now)
        return await self._issue(agent, now)

    async def rotate(self, name: str) -> str:
        """Give the agent NAME a new key. The old key stops at once."""
        agent = await self._active_agent(name)
        return await self._issue(agent.login, datetime.now(UTC))

    async def remove(self, name: str) -> None:
        """Remove the key of the agent NAME and revoke the agent and its sessions."""
        agent = await self._active_agent(name)
        now = datetime.now(UTC)
        await self._keys.remove(agent.login)
        for session in await self._store.sessions(agent.login):
            if session.status in ("issued", "active"):
                await self._store.set_status(session.login, "revoked", now)
        await self._store.set_status(agent.login, "revoked", now)

    async def check(self, key: object) -> str:
        """The agent name of a valid agent key. Raises CredentialError("BAD_KEY")."""
        try:
            login, secret = parse_credential(key)
        except CredentialError:
            raise CredentialError("BAD_KEY") from None
        principal = await self._store.get(login)
        if (
            principal is None
            or principal.kind != "agent"
            or principal.status != "active"
        ):
            raise CredentialError("BAD_KEY")
        if not self._hasher.verify(secret, await self._keys.get_hash(login)):
            raise CredentialError("BAD_KEY")
        return login

    async def _active_agent(self, name: str) -> Principal:
        principal = await self._store.get(name)
        if (
            principal is None
            or principal.kind != "agent"
            or principal.status != "active"
        ):
            raise UnknownAccountError(f"no such agent: {name}")
        return principal

    async def _issue(self, agent: str, now: datetime) -> str:
        secret = new_secret()
        await self._keys.set_hash(agent, self._hasher.hash(secret), now)
        return format_credential(agent, secret)
