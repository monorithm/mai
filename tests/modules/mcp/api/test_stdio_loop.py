import io
import json

import anyio

from mai.modules.mcp import Dispatcher, StdioLoop

from ..fakes import fake_registry, resolver


def test_stdio_transport_lines(monkeypatch):
    monkeypatch.delenv("MAI_CREDENTIAL_FILE", raising=False)
    stdin = io.BytesIO(
        b'{"jsonrpc":"2.0","id":1,"method":"tools/list"}\nnot json\n\n'
        b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n'
        + b"x" * (1024 * 1024 + 5)
        + b"\n"
    )
    out = io.BytesIO()
    loop = StdioLoop(Dispatcher(fake_registry(), resolver), stdin, out)
    assert anyio.run(loop.run) == 0
    lines = [json.loads(x) for x in out.getvalue().splitlines()]
    assert (
        len(lines) == 3
        and lines[1]["error"]["code"] == -32700
        and lines[2]["error"]["code"] == -32600
    )
