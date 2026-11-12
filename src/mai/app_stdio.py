"""The MCP stdio entry point: one process for each conversation, with the Dispatcher of the application."""

import sys

import anyio
from bustan import create_app_context

from .app_module import AppModule
from .modules.mcp import Dispatcher, StdioLoop


async def serve_stdio(stdin=None, stdout=None) -> int:
    """Build the application context, run the stdio loop until stdin closes, then close the context."""
    context = create_app_context(AppModule)
    await context.init()
    try:
        return await StdioLoop(context.get(Dispatcher), stdin, stdout).run()
    finally:
        await context.close()


def main(argv: list[str] | None = None) -> int:
    del argv
    return anyio.run(serve_stdio)


if __name__ == "__main__":
    sys.exit(main())
