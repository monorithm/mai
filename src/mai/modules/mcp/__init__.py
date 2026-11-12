"""The MCP module: the JSON-RPC dispatcher of the 22 tools, over stdio and stateless HTTP."""

from .api.stdio_loop import StdioLoop
from .application.dispatcher import Dispatcher, McpConnection

__all__ = ["Dispatcher", "McpConnection", "McpModule", "StdioLoop"]


def __getattr__(name: str):
    # McpModule imports the sessions, mail, threads, and board modules. The import waits until a caller
    # asks for McpModule, thus the dispatcher and the transports load and test without those modules.
    if name == "McpModule":
        from .mcp_module import McpModule

        return McpModule
    raise AttributeError(name)
