"""The mail tools of the MCP contract: tool name to an async callable(ctx, args) -> dict."""

from __future__ import annotations

import functools

from bustan import Injectable

from mai.platform.contract import ToolError

from ..application.list_addresses_query import ListAddressesHandler
from ..application.list_inbox_query import ListInboxHandler
from ..application.mark_done_command import MarkDoneHandler
from ..application.read_message_query import ReadMessageHandler
from ..application.read_thread_query import ReadThreadHandler
from ..application.reply_command import ReplyHandler
from ..application.send_message_command import SendMessageHandler
from ..application.wait_for_mail_query import WaitForMailHandler
from ..domain.errors import MailError


def _contract(execute):
    """Change a MailError of the domain into the ToolError of the contract."""

    @functools.wraps(execute)
    async def tool(ctx, args):
        try:
            return await execute(ctx, args if isinstance(args, dict) else {})
        except MailError as exc:
            raise ToolError(exc.code, path=exc.path, **exc.extra) from None

    return tool


@Injectable()
class MailTools:
    def __init__(
        self,
        list_addresses: ListAddressesHandler,
        send_message: SendMessageHandler,
        reply: ReplyHandler,
        list_inbox: ListInboxHandler,
        read_message: ReadMessageHandler,
        read_thread: ReadThreadHandler,
        wait_for_mail: WaitForMailHandler,
        mark_done: MarkDoneHandler,
    ) -> None:
        self._tools = {
            "list_addresses": _contract(list_addresses.execute),
            "send_message": _contract(send_message.execute),
            "reply": _contract(reply.execute),
            "list_inbox": _contract(list_inbox.execute),
            "read_message": _contract(read_message.execute),
            "read_thread": _contract(read_thread.execute),
            "wait_for_mail": _contract(wait_for_mail.execute),
            "mark_done": _contract(mark_done.execute),
        }

    def tools(self) -> dict:
        return dict(self._tools)
