"""The tool list_inbox."""

from bustan import Injectable

from ..domain import mail_rules as r
from .mail_reader import MailReader, boxes_of, by_date


@Injectable()
class ListInboxHandler:
    def __init__(self, reader: MailReader) -> None:
        self._reader = reader

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        include_queue = r.bool_arg(args, "include_queue", True)
        include_done = r.bool_arg(args, "include_done", False)
        unread_only = r.bool_arg(args, "unread_only", False)
        limit = r.limit_arg(args, 50, r.MAX_LIST)
        thread_ref = r.check_thread_ref(args.get("thread_ref"))
        criteria = "UNSEEN" if unread_only else "ALL"
        async with self._reader.lock(state):
            entries = await self._reader.scan(
                state,
                boxes_of(state, with_queue=include_queue, done=include_done),
                criteria,
            )
        messages = []
        for entry in by_date(entries):
            summary = self._reader.summary(state, entry)
            if unread_only and summary["status"] not in ("unread", "notified"):
                continue
            if thread_ref and summary["thread_ref"] != thread_ref:
                continue
            messages.append(summary)
            if len(messages) >= limit:
                break
        return {"notice": r.NOTICE, "address": state.address, "messages": messages}
