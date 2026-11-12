"""Read the board for the owner page, through the board tools."""

from __future__ import annotations

from dataclasses import dataclass

from bustan import Injectable

from mai.modules.board import BoardTools
from mai.platform.contract import Ctx


@dataclass(frozen=True, slots=True)
class BoardSnapshotQuery:
    pass


@Injectable()
class BoardSnapshotHandler:
    def __init__(self, board: BoardTools) -> None:
        self._board = board

    async def execute(self, query: BoardSnapshotQuery) -> dict:
        tool = self._board.tools().get("get_board")
        if tool is None:
            return {}
        return await tool(Ctx(session_state=None, transport="http"), {})
