"""The memory board store. The board needs PostgreSQL (its rows join the mail_auth tables), thus this adapter
only holds the place of the port for host tests: each call fails with STORE_UNAVAILABLE."""

from mai.platform.contract import ToolError

from ..domain.board_store import BoardStore


class MemoryBoardStore(BoardStore):
    async def call(self, tool, ctx, args, state):
        raise ToolError("STORE_UNAVAILABLE")
