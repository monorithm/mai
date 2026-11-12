"""POST /mcp through the bustan application, with a fake registry and a fake resolver."""

import json

import pytest
from bustan import Module, ValueProvider
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.mcp import Dispatcher
from mai.modules.mcp.api.mcp_controller import McpController
from mai.modules.mcp.api.mcp_http_policy import McpHttpPolicy
from mai.modules.mcp.application.wait_lane import WaitLane

from ..fakes import HANDLE, fake_registry, resolver

HOST = "127.0.0.1:8080"
LANE = WaitLane(total=1, per_key=1)


@Module(
    controllers=[McpController],
    providers=[
        ValueProvider(
            provide=Dispatcher,
            use_value=Dispatcher(fake_registry(), resolver, resolver),
        ),
        ValueProvider(
            provide=McpHttpPolicy,
            use_value=McpHttpPolicy(
                frozenset({HOST}), frozenset({"http://localhost:8765"})
            ),
        ),
        ValueProvider(provide=WaitLane, use_value=LANE),
    ],
)
class McpTestModule:
    pass


@pytest.fixture
def client():
    with AsgiTestClient(create_test_app(McpTestModule)) as c:
        yield c


def post(client, body, **headers):
    data = body if isinstance(body, (bytes, str)) else json.dumps(body)
    return client.post(
        "/mcp",
        content=data,
        headers={"host": HOST, "content-type": "application/json", **headers},
    )


LIST = {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}


def test_get_and_delete_are_405(client):
    for method in (client.get, client.delete):
        r = method("/mcp", headers={"host": HOST})
        assert r.status_code == 405 and r.headers["allow"] == "POST"


def test_post_lists_tools(client):
    r = post(client, LIST)
    assert r.status_code == 200 and len(r.json()["result"]["tools"]) == 21
    assert r.headers["cache-control"] == "no-store"


def test_host_and_origin_rules(client):
    assert post(client, LIST, origin="http://evil.example").status_code == 403
    assert post(client, LIST, origin="http://localhost:8765").status_code == 200
    assert post(client, LIST, host="evil.example").status_code == 403
    assert client.get("/mcp", headers={"host": "evil.example"}).status_code == 403


def test_body_limit_and_parse_error(client):
    assert post(client, b"{" + b" " * (1024 * 1024 + 1) + b"}").status_code == 413
    r = post(client, b"not json")
    assert r.status_code == 400 and r.json()["error"]["code"] == -32700


def test_notification_is_202(client):
    r = post(client, {"jsonrpc": "2.0", "method": "notifications/initialized"})
    assert r.status_code == 202 and r.content == b""


def test_lease_bearer_and_busy_lane(client):
    call = {
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {"name": "list_inbox", "arguments": {}},
    }
    auth = {"authorization": f"Bearer {HANDLE}"}
    r = post(client, call, **auth)
    assert r.json()["result"]["structuredContent"]["login"] == "cursor.lane-a"
    refused = post(client, call)
    assert refused.status_code == 401
    assert refused.json()["error"]["code"] == "LEASE_UNKNOWN"
    wait = {
        "jsonrpc": "2.0",
        "id": 4,
        "method": "tools/call",
        "params": {"name": "wait_for_mail", "arguments": {}},
    }
    assert LANE.enter(HANDLE)
    try:
        r = post(client, wait, **auth)
        assert r.json()["result"]["structuredContent"]["error"]["code"] == "BUSY"
    finally:
        LANE.leave(HANDLE)
    assert not post(client, wait, **auth).json()["result"].get("isError")
