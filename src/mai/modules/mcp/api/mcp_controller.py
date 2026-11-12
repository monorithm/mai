"""The Streamable HTTP transport of MCP: stateless POST /mcp.

There is no MCP session. Each tool call carries the argument session or the header Mai-Session.
GET and DELETE answer 405, because the server opens no stream and keeps no session to end.
"""

import re
import sys

from bustan import Controller, Delete, Get, HttpRequest, HttpResponse, Post

from ..application.dispatcher import Dispatcher
from ..application.json_rpc import encode, error, parse_request
from ..application.wait_lane import WaitLane
from .mcp_http_policy import McpHttpPolicy

_DIGITS = re.compile(r"[0-9]+\Z")
_FORBIDDEN = {
    "jsonrpc": "2.0",
    "id": None,
    "error": {"code": -32600, "message": "Forbidden"},
}


def _reply(
    status: int,
    body: dict | None = None,
    headers: dict[str, str] | None = None,
    close: bool = False,
) -> HttpResponse:
    head = {"cache-control": "no-store"}
    if body is not None:
        head["content-type"] = "application/json"
    if close:
        head["connection"] = "close"
    head.update(headers or {})
    return HttpResponse(
        status_code=status, headers=head, body=b"" if body is None else encode(body)
    )


@Controller("/mcp")
class McpController:
    """Checks Host, Origin, and the body length, then gives the JSON-RPC message to the dispatcher."""

    def __init__(
        self, dispatcher: Dispatcher, policy: McpHttpPolicy, waits: WaitLane
    ) -> None:
        self._dispatcher = dispatcher
        self._policy = policy
        self._waits = waits

    def _guard(self, request: HttpRequest) -> HttpResponse | None:
        headers = request.headers
        if not self._policy.admits(headers.get("host"), headers.get("origin")):
            return _reply(403, _FORBIDDEN, close=True)
        return None

    @Get("/")
    async def stream(self, request: HttpRequest) -> HttpResponse:
        return self._guard(request) or _reply(
            405, {"error": "method not allowed"}, {"allow": "POST"}
        )

    @Delete("/")
    async def end_session(self, request: HttpRequest) -> HttpResponse:
        return self._guard(request) or _reply(
            405, {"error": "method not allowed"}, {"allow": "POST"}
        )

    @Post("/")
    async def call(self, request: HttpRequest) -> HttpResponse:
        refused = self._guard(request)
        if refused is not None:
            return refused
        headers = request.headers
        length = (headers.get("content-length") or "").strip()
        if headers.get("transfer-encoding") is not None or not _DIGITS.match(length):
            return _reply(411, {"error": "bad length"}, close=True)
        if int(length) > self._policy.max_request:
            return _reply(413, {"error": "request too large"}, close=True)
        raw = await request.body()
        if len(raw) > self._policy.max_request:
            return _reply(413, {"error": "request too large"}, close=True)
        try:
            msg = parse_request(raw)
        except (ValueError, RecursionError, UnicodeDecodeError):
            return _reply(400, error(None, -32700, "Parse error"))
        key = WaitLane.key_of(msg, headers)
        if key is not None and not self._waits.enter(key):
            return _reply(200, WaitLane.busy(msg))
        try:
            status, body = await self._dispatcher.handle(
                msg, None, "http", dict(headers.items())
            )
        except Exception as exc:  # noqa: BLE001 - a defect must give 500 with no internal text
            sys.stderr.write(f"mai: internal error of type {type(exc).__name__}\n")
            status, body = 500, error(None, -32603, "Internal error")
        finally:
            if key is not None:
                self._waits.leave(key)
        return _reply(status, body)
