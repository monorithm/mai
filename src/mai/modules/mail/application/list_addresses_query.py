"""The tool list_addresses: the agents and their live sessions."""

from bustan import Injectable

from ..domain import mail_rules as r
from ..domain.ports import Directory
from .mail_reader import MailReader


@Injectable()
class ListAddressesHandler:
    def __init__(self, reader: MailReader, directory: Directory) -> None:
        self._reader = reader
        self._directory = directory

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        agents = await self._directory.agents()
        return {
            "me": state.address,
            "agents": [
                {
                    "address": f"{a}@{r.DOMAIN}",
                    "sessions": [
                        {
                            "address": f"{s.login}@{r.DOMAIN}",
                            "label": s.label,
                            "last_seen": r.iso(s.last_login_at),
                        }
                        for s in agents[a]
                    ],
                }
                for a in sorted(agents)
            ],
        }
