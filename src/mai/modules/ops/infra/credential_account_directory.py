"""The account directory of the import: the accounts of the credentials module."""

from __future__ import annotations

from bustan import Injectable

from mai.modules.credentials import CredentialService

from ..domain.ports import AccountDirectory


@Injectable()
class CredentialAccountDirectory(AccountDirectory):
    def __init__(self, credentials: CredentialService) -> None:
        self._credentials = credentials

    async def exists(self, account: str) -> bool:
        return await self._credentials.account_kind(account) is not None

    async def add_agent(self, name: str) -> None:
        await self._credentials.agent_add(name)
