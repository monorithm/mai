"""The tool reply. A reply to a thread in the agent queue claims the thread first."""

from bustan import Injectable

from mai.modules.threads import ClaimService

from ..domain import mail_rules as r
from ..domain.errors import MailError
from .mail_reader import MailReader, queue_of
from .mail_sender import MailSender


@Injectable()
class ReplyHandler:
    def __init__(
        self, reader: MailReader, sender: MailSender, claims: ClaimService
    ) -> None:
        self._reader = reader
        self._sender = sender
        self._claims = claims

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        reply_all = r.bool_arg(args, "reply_all", False)
        _, body, kind, priority, expects, request_id, item = r.check_text(
            {**args, "subject": None}, "reply"
        )
        async with self._reader.lock(state):
            entry = await self._reader.find(state, args.get("ref"))
        h = entry["headers"]
        to_l, cc_l = r.reply_targets(h, state.address, reply_all)
        if not to_l:
            raise MailError("RECIPIENT_UNKNOWN", path="ref")
        subject = r.reply_subject(h.get("subject"))
        if item is None:
            item = r.item_of(h)
        references = r.reply_references(h, entry["message_id"])
        if entry["mailbox"] == queue_of(state):
            # The claim refuses (THREAD_CLAIMED) when a different session holds the thread.
            await self._claims.claim(state, thread_ref=r.thread_ref_of(entry["root"]))
        return await self._sender.deliver(
            state,
            to_l,
            cc_l,
            subject,
            body,
            kind,
            priority,
            expects,
            item,
            request_id,
            in_reply_to=entry["message_id"],
            references=references,
        )
