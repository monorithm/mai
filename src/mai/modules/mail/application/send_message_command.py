"""The tool send_message."""

from bustan import Injectable

from ..domain import mail_rules as r
from ..domain.errors import MailError
from .mail_reader import MailReader
from .mail_sender import MailSender


@Injectable()
class SendMessageHandler:
    def __init__(self, reader: MailReader, sender: MailSender) -> None:
        self._reader = reader
        self._sender = sender

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        to = r.recipients(args, "to", True)
        cc = r.recipients(args, "cc", False)
        if not isinstance(args.get("subject"), str) or not args.get("subject"):
            raise MailError("INVALID_ARGUMENT", path="subject")
        subject, body, kind, priority, expects, request_id, item = r.check_text(
            args, "request"
        )
        await self._sender.check_known(to, "to")
        await self._sender.check_known(cc, "cc")
        return await self._sender.deliver(
            state, to, cc, subject, body, kind, priority, expects, item, request_id
        )
