"""The JSON-RPC dispatcher of the mai MCP server, for stdio and for stateless HTTP.

It answers initialize, server/discover, ping, tools/list, and tools/call. A tool call goes through input
validation, session resolution, and the role check before the tool runs. Each tool result has
structuredContent and one text item.
"""

import inspect
import sys
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field

from mai.platform import supervisor
from mai.platform.contract import Ctx, ToolError

from ..domain import agent_guide
from ..domain import tool_schemas as schemas
from .json_rpc import (
    CAPABILITIES,
    INITIALIZE_VERSIONS,
    INSTRUCTIONS,
    KEY_SERVER,
    KEY_VERSION,
    SERVER_INFO,
    SESSION_FREE,
    SUPPORTED,
    check_one_of,
    echo_id,
    error,
    error_result,
    header_of,
    ok,
    tool_result,
    valid_id,
    validate,
)

ToolFunc = Callable[[Ctx, dict], Awaitable[dict]]
Resolver = Callable[[str], Awaitable[object]]
LeaseResolver = Callable[[str], Awaitable[object]]
Notify = Callable[[dict], object]


@dataclass
class McpConnection:
    """The state of one stdio conversation: the handle of its last login, and the sink of notifications.

    HTTP has no MCP session, thus an HTTP call passes no connection.
    """

    handle: str | None = None
    notify: Notify | None = field(default=None, repr=False)


class LeaseRequired(Exception):
    """An HTTP tool call has no valid lease. The transport answers HTTP 401 LEASE_UNKNOWN."""


def bearer_of(headers: Mapping[str, str] | None) -> str:
    """The bearer token of the header Authorization, or "" when there is none."""
    value = header_of(headers, "Authorization") or ""
    scheme, _, token = value.partition(" ")
    return token.strip() if scheme.lower() == "bearer" else ""


class Dispatcher:
    """Maps one JSON-RPC message to a status code and a response body.

    registry maps a tool name to an async callable(ctx, args) -> dict. resolver maps a session handle to a
    session state, or raises ToolError(SESSION_UNKNOWN). The status code is for HTTP. A body of None means
    no response (a notification).
    """

    def __init__(
        self,
        registry: Mapping[str, ToolFunc],
        resolver: Resolver,
        lease_resolver: LeaseResolver | None = None,
    ) -> None:
        self._registry = dict(registry)
        self._resolver = resolver
        self._lease_resolver = lease_resolver

    @property
    def registry(self) -> dict[str, ToolFunc]:
        return self._registry

    async def handle(
        self,
        message,
        session_state: McpConnection | None = None,
        transport: str = "http",
        headers: Mapping[str, str] | None = None,
    ) -> tuple[int, dict | None]:
        if transport == "stdio" and session_state is None:
            raise ValueError("a stdio call needs its McpConnection")
        msg = message
        if (
            not isinstance(msg, dict)
            or msg.get("jsonrpc") != "2.0"
            or not isinstance(msg.get("method"), str)
        ):
            return 400, error(echo_id(msg), -32600, "Invalid Request")
        if "id" not in msg:
            return 202, None
        if not valid_id(msg["id"]):
            return 400, error(None, -32600, "Invalid Request")
        request_id, method, params = msg["id"], msg["method"], msg.get("params")
        params = {} if params is None else params
        if not isinstance(params, dict):
            return 400, error(request_id, -32602, "params must be an object")
        raw_meta = params.get("_meta")
        meta: dict = raw_meta if isinstance(raw_meta, dict) else {}
        modern = KEY_VERSION in meta
        if modern and meta.get(KEY_VERSION) not in SUPPORTED:
            return 400, error(
                request_id,
                -32022,
                "Unsupported protocol version",
                {
                    "supported": list(SUPPORTED),
                    "requested": str(meta.get(KEY_VERSION))[:40],
                },
            )
        extra = {"_meta": {KEY_SERVER: SERVER_INFO}} if modern else {}
        if method == "initialize":
            asked = params.get("protocolVersion")
            version = asked if asked in SUPPORTED else INITIALIZE_VERSIONS[0]
            return 200, ok(
                request_id,
                {
                    "protocolVersion": version,
                    "capabilities": CAPABILITIES,
                    "serverInfo": SERVER_INFO,
                    "instructions": INSTRUCTIONS,
                },
            )
        if method == "server/discover":
            return 200, ok(
                request_id,
                dict(
                    extra,
                    supportedVersions=list(SUPPORTED),
                    capabilities=CAPABILITIES,
                    serverInfo=SERVER_INFO,
                    instructions=INSTRUCTIONS,
                ),
            )
        if method == "ping":
            return 200, ok(request_id, {})
        if method == "resources/list":
            # No session: the resources have only text of the server, not mail.
            return 200, ok(
                request_id, dict(extra, resources=list(agent_guide.RESOURCES))
            )
        if method == "resources/templates/list":
            return 200, ok(request_id, dict(extra, resourceTemplates=[]))
        if method == "resources/read":
            uri = params.get("uri")
            if not isinstance(uri, str) or not uri:
                return 200, error(request_id, -32602, "uri must be a string")
            contents = agent_guide.read_resource(uri)
            if contents is None:
                return 200, error(
                    request_id, -32002, "Resource not found", {"uri": uri[:200]}
                )
            return 200, ok(request_id, dict(contents, **extra))
        if method == "tools/list":
            tools = await self.list_tools(session_state, transport)
            return 200, ok(request_id, dict(extra, tools=tools))
        if method == "tools/call":
            name = params.get("name")
            if not isinstance(name, str) or name not in schemas.BY_NAME:
                return 200, error(request_id, -32602, "Unknown tool")
            args = params.get("arguments")
            args = {} if args is None else args
            result = await self.call_tool(name, args, session_state, transport, headers)
            return 200, ok(request_id, dict(result, **extra))
        return 200, error(request_id, -32601, "Method not found")

    async def list_tools(
        self, conn: McpConnection | None, transport: str
    ) -> list[dict]:
        if transport == "http":
            return [schemas.public_view(t) for t in schemas.TOOLS]
        state = await self._stdio_state(conn)
        if state is None:
            return [schemas.public_view(schemas.BY_NAME[n]) for n in SESSION_FREE]
        roles = getattr(state, "roles", {})
        return [
            schemas.public_view(t) for t in schemas.TOOLS if schemas.allowed(t, roles)
        ]

    async def _stdio_state(self, conn: McpConnection | None):
        if conn is None or conn.handle is None:
            return None
        try:
            return await self._resolver(conn.handle)
        except ToolError:
            conn.handle = None
            return None

    async def call_tool(
        self,
        name: str,
        args,
        conn: McpConnection | None = None,
        transport: str = "http",
        headers: Mapping[str, str] | None = None,
    ) -> dict:
        try:
            return tool_result(await self._call(name, args, conn, transport, headers))
        except LeaseRequired:
            raise
        except ToolError as exc:
            return error_result(exc)
        except Exception as exc:  # noqa: BLE001 - A tool defect must not end the process or show internal text.
            sys.stderr.write(
                f"mai: internal error in {name} of type {type(exc).__name__}\n"
            )
            return error_result(ToolError("INTERNAL"))

    async def _call(self, name, args, conn, transport, headers):
        if supervisor.held():
            # A restore holds the daemons: a stdio process in the container must not write now.
            raise ToolError("MAIL_SERVER_DOWN")
        spec = schemas.BY_NAME[name]
        if not isinstance(args, dict):
            raise ToolError("INVALID_ARGUMENT", path="arguments")
        validate(spec["inputSchema"], args)
        check_one_of(name, args)
        if name not in schemas.BRIDGE_TOOLS:
            # The bridge selects the lease of the call from the handle in "session". The tool
            # never gets the argument: the lease of the call gives the session.
            args = {key: value for key, value in args.items() if key != "session"}
        state = None
        if transport == "http":
            state = await self._lease_state(headers)
        if name in schemas.BRIDGE_TOOLS:
            # The bridge "mai mcp" answers login and logout. This call did not go through it.
            raise ToolError("BRIDGE_ONLY")
        if transport != "http":
            state = await self._stdio_call_state(name, conn)
        if state is not None and not schemas.allowed(spec, getattr(state, "roles", {})):
            raise ToolError("FORBIDDEN_ROLE")
        func = self._registry.get(name)
        if func is None:
            raise ToolError("INTERNAL")
        result = func(Ctx(state, transport), args)
        if inspect.isawaitable(result):
            result = await result
        if not isinstance(result, dict):
            raise ToolError("INTERNAL")
        return result

    async def _stdio_call_state(self, name: str, conn: McpConnection | None) -> object:
        """The session state of a stdio call from the handle of the connection.

        Only whoami runs with no session.
        """
        handle = conn.handle if conn is not None else None
        if handle is None:
            if name != "whoami":
                raise ToolError("NOT_LOGGED_IN", path="session")
            return None
        try:
            return await self._resolver(handle)
        except ToolError:
            if name != "whoami":
                raise
            if conn is not None and handle == conn.handle:
                conn.handle = None
            return None

    async def _lease_state(self, headers: Mapping[str, str] | None) -> object:
        """The session state of the lease of the call. Raises LeaseRequired."""
        token = bearer_of(headers)
        if self._lease_resolver is None or not token:
            raise LeaseRequired()
        try:
            return await self._lease_resolver(token)
        except Exception:  # noqa: BLE001 - each failure of the lease check gives LEASE_UNKNOWN
            raise LeaseRequired() from None
