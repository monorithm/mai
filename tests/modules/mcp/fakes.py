"""A fake tool registry and a fake session resolver for the tests of the MCP module."""

from mai.modules.mcp.domain import tool_schemas as schemas
from mai.platform.contract import ToolError

HANDLE = "ses-" + "a" * 26
SUPER = "ses-" + "b" * 26


class State:
    def __init__(self, login, roles):
        self.login, self.agent, self.roles, self.handle = (
            login,
            login.split(".")[0],
            roles,
            None,
        )


STATES = {
    HANDLE: State("cursor.lane-a", {"api": "worker"}),
    SUPER: State("codex.main", {"api": "supervisor"}),
}


async def resolver(handle):
    if handle not in STATES:
        raise ToolError("SESSION_UNKNOWN", path="session")
    return STATES[handle]


async def lease_resolver(token):
    """A lease token of the fake is a handle of STATES. Each other token is not a valid lease."""
    if token not in STATES:
        raise LookupError("no such lease")
    return STATES[token]


def fake_registry():
    def echo(name):
        async def tool(ctx, args):
            return {
                "tool": name,
                "login": getattr(ctx.session_state, "login", None),
                "transport": ctx.transport,
            }

        return tool

    reg = {t["name"]: echo(t["name"]) for t in schemas.TOOLS}

    async def boom(ctx, args):
        raise RuntimeError("secret detail")

    reg["mark_done"] = boom
    return reg
