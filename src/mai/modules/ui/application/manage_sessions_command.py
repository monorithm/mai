"""List, make, rotate, and revoke agent sessions for the owner, and list the agents."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

from bustan import Injectable

from mai.modules.credentials import CredentialService


@dataclass(frozen=True, slots=True)
class ManageSessionsCommand:
    action: Literal["list", "agents", "new", "rotate", "revoke"]
    agent: str = ""
    label: str = ""
    days: int = 7
    login: str = ""


@Injectable()
class ManageSessionsHandler:
    def __init__(self, credentials: CredentialService) -> None:
        self._credentials = credentials

    async def execute(self, command: ManageSessionsCommand) -> dict | list[dict]:
        c = self._credentials
        match command.action:
            case "list":
                return await c.session_list(command.agent or None)
            case "agents":
                return [{"agent": name} for name in await c.agent_names()]
            case "new":
                return await c.session_new(
                    command.agent, command.label, expires_days=command.days
                )
            case "rotate":
                return await c.session_rotate(command.login)
            case "revoke":
                return await c.session_revoke(command.login)
        raise ValueError(f"unknown action {command.action}")
