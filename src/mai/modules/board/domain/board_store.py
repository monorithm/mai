"""The port of the board store: it runs one board tool call against the board state."""

from abc import ABC, abstractmethod


class BoardStore(ABC):
    """Runs a board tool for a resolved session and returns the structured result of the tool."""

    @abstractmethod
    async def call(self, tool: str, ctx, args: dict, state) -> dict:
        """Run the tool. Raise ToolError for a contract failure."""
