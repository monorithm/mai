"""The tool mark_done: flag the message done and move it to the mailbox Done."""

from bustan import Injectable

from ..domain import mail_rules as r
from .mail_reader import MailReader, boxes_of


@Injectable()
class MarkDoneHandler:
    def __init__(self, reader: MailReader) -> None:
        self._reader = reader

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        ref = args.get("ref")
        async with self._reader.lock(state):
            entry = await self._reader.find(state, ref, boxes_of(state, done=True))
            if entry["mailbox"] == "Done":
                if r.DONE_FLAG not in entry["flags"]:
                    await self._reader.call(
                        state,
                        "store_flags",
                        "Done",
                        [entry["uid"]],
                        add=(r.DONE_FLAG, r.SEEN),
                    )
                return {"ref": ref, "status": "done"}
            await self._reader.call(
                state,
                "store_flags",
                entry["mailbox"],
                [entry["uid"]],
                add=(r.DONE_FLAG, r.SEEN),
            )
            moved = await self._reader.call(
                state, "move", entry["mailbox"], entry["uid"], "Done"
            )
        return {"ref": ref, "status": "done" if moved else "seen"}
