"""The tool read_message. It marks the message seen."""

from bustan import Injectable

from ..domain import mail_rules as r
from .mail_reader import MailReader


@Injectable()
class ReadMessageHandler:
    def __init__(self, reader: MailReader) -> None:
        self._reader = reader

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        async with self._reader.lock(state):
            entry = await self._reader.find(state, args.get("ref"))
            message = await self._reader.full(state, entry)
            if r.SEEN not in entry["flags"]:
                await self._reader.call(
                    state,
                    "store_flags",
                    entry["mailbox"],
                    [entry["uid"]],
                    add=(r.SEEN,),
                )
                entry["flags"].append(r.SEEN)
                message["status"] = r.status_of(entry["flags"], entry["mailbox"])
        return {"notice": r.NOTICE, "message": message}
