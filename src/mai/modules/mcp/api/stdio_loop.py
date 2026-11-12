"""The stdio transport: newline-delimited JSON-RPC on stdin and stdout, one process for each conversation."""

import os
import sys
import threading

import anyio

from ..application.dispatcher import Dispatcher, McpConnection
from ..application.json_rpc import encode, error, parse_request

MAX_REQUEST = 1024 * 1024


class StdioLoop:
    """Reads one request on each line, and writes one response line for each request that has an id."""

    def __init__(self, dispatcher: Dispatcher, stdin=None, stdout=None) -> None:
        self._dispatcher = dispatcher
        self._stdin = stdin or sys.stdin.buffer
        self._stdout = stdout or sys.stdout.buffer
        self._lock = threading.Lock()
        self.connection = McpConnection(notify=self._write)

    def _readline(self, limit: int) -> bytes:
        return self._stdin.readline(limit)

    def _write(self, body: dict) -> None:
        with self._lock:
            self._stdout.write(encode(body) + b"\n")
            self._stdout.flush()

    async def run(self) -> int:
        await self._login_from_env()
        while True:
            raw = await anyio.to_thread.run_sync(self._readline, MAX_REQUEST + 1)
            if not raw:
                return 0
            if len(raw) > MAX_REQUEST:
                while raw and not raw.endswith(b"\n"):
                    raw = await anyio.to_thread.run_sync(self._readline, 65536)
                body = error(None, -32600, "Invalid Request")
            elif not raw.strip():
                continue
            else:
                try:
                    msg = parse_request(raw)
                except (ValueError, RecursionError):
                    body = error(None, -32700, "Parse error")
                else:
                    body = (
                        await self._dispatcher.handle(msg, self.connection, "stdio")
                    )[1]
            if body is not None:
                self._write(body)

    async def _login_from_env(self) -> None:
        """Log in at start from MAI_CREDENTIAL or MAI_CREDENTIAL_FILE: the session then does not call login.

        The host shim reads the credential file on the host and gives its text in
        MAI_CREDENTIAL (docker exec -e), because a host path is not a path in the
        container. The process removes the variable at once, thus no child gets it.
        """
        credential = os.environ.pop("MAI_CREDENTIAL", None)
        path = os.environ.get("MAI_CREDENTIAL_FILE")
        if credential:
            args = {"credential": credential.strip()}
        elif path:
            args = {"credential_file": path}
        else:
            return
        args["client"] = os.environ.get("MAI_CLIENT", "env")
        result = await self._dispatcher.call_tool(
            "login", args, self.connection, "stdio"
        )
        if result.get("isError"):
            code = result["structuredContent"]["error"]["code"]
            sys.stderr.write(f"mai: the login at start failed: {code}\n")
