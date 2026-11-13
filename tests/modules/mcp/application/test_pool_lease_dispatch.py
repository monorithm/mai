"""MCP calls on a pool lease with no session, with the real lease service."""

import json

import anyio
import pytest
from bustan import Module, ValueProvider
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.mcp import Dispatcher
from mai.modules.mcp.api.mcp_controller import McpController
from mai.modules.mcp.api.mcp_http_policy import McpHttpPolicy
from mai.modules.mcp.application.dispatcher import LeaseRequired
from mai.modules.mcp.application.wait_lane import WaitLane
from mai.modules.sessions import SessionTools
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from tests.modules.sessions.test_lease_service import Parts, make_parts

HOST = "127.0.0.1:8080"
NO_FREE_SESSION = {
    "error": {
        "code": "NO_FREE_SESSION",
        "message": (
            "No pool session of the agent is free. "
            "Call login with a named session, or try again later."
        ),
        "path": None,
        "retry_safe": True,
    }
}


def make_dispatcher(parts: Parts) -> Dispatcher:
    registry = dict(SessionTools(parts.sessions).tools())

    async def list_inbox(ctx, args):
        return {"login": ctx.session_state.login}

    registry["list_inbox"] = list_inbox
    return Dispatcher(
        registry, parts.sessions.resolve, parts.leases.state, parts.leases.check
    )


def full_pool(tmp_path) -> tuple[Parts, list[dict], str]:
    """The 8 pool leases of cursor, and the token of a 9th lease with no session."""
    parts = make_parts(tmp_path)
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(MAX_SESSIONS)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 9)
    assert extra["session"] is None
    return parts, pool, extra["lease"]


def send(d, token, method, params=None):
    msg = {"jsonrpc": "2.0", "id": 1, "method": method, "params": params or {}}

    async def go():
        return await d.handle(msg, None, "http", {"Authorization": f"Bearer {token}"})

    return anyio.run(go)


def tool(d, token, name, args=None):
    return send(d, token, "tools/call", {"name": name, "arguments": args or {}})


def beat(parts, token):
    return anyio.run(parts.leases.heartbeat, token)


def test_the_methods_with_no_session_work_on_a_lease_with_no_session(tmp_path):
    parts, pool, token = full_pool(tmp_path)
    anyio.run(parts.leases.end, pool[0]["lease"])
    d = make_dispatcher(parts)
    status, body = send(d, token, "tools/list")
    assert status == 200 and len(body["result"]["tools"]) == 22
    for method, params in (
        ("initialize", {"protocolVersion": "2025-11-25"}),
        ("ping", {}),
        ("resources/list", {}),
        ("resources/read", {"uri": "mai://guide"}),
        ("prompts/list", {}),
    ):
        status, body = send(d, token, method, params)
        assert status == 200 and body["id"] == 1, method

    async def notify():
        msg = {"jsonrpc": "2.0", "method": "notifications/initialized"}
        return await d.handle(msg, None, "http", {"Authorization": f"Bearer {token}"})

    assert anyio.run(notify) == (202, None)
    # No method of this test needs a session, thus the lease has none.
    assert beat(parts, token) == {"session": None, "new_mail": 0}


def test_a_tool_call_with_a_full_pool_gives_no_free_session_and_http_200(tmp_path):
    parts, _pool, token = full_pool(tmp_path)
    d = make_dispatcher(parts)
    for name in ("whoami", "list_inbox"):
        status, body = tool(d, token, name)
        assert status == 200
        result = body["result"]
        assert result["isError"] is True
        assert result["structuredContent"] == NO_FREE_SESSION
        assert json.loads(result["content"][0]["text"]) == NO_FREE_SESSION
    assert beat(parts, token) == {"session": None, "new_mail": 0}


def test_a_tool_call_binds_a_free_pool_session_one_time(tmp_path):
    parts, pool, token = full_pool(tmp_path)
    d = make_dispatcher(parts)
    assert tool(d, token, "whoami")[1]["result"]["isError"] is True
    anyio.run(parts.leases.end, pool[5]["lease"])
    logins = len(parts.factory.opened)
    me = tool(d, token, "whoami")[1]["result"]["structuredContent"]
    assert me["logged_in"] is True and me["login"] == "cursor.s6"
    assert me["agent"] == "cursor"
    assert beat(parts, token) == {"session": "cursor.s6", "new_mail": 3}
    listed = tool(d, token, "list_inbox")[1]["result"]["structuredContent"]
    assert listed == {"login": "cursor.s6"}
    assert len(parts.factory.opened) == logins + 1
    # The bind took the free session: a new pool lease gets no session.
    assert anyio.run(parts.leases.acquire, parts.key, "x", "/w", 20)["session"] is None


def test_a_tool_call_on_a_lease_with_no_session_of_a_removed_agent_needs_a_new_lease(
    tmp_path,
):
    parts, pool, token = full_pool(tmp_path)
    anyio.run(parts.leases.end, pool[0]["lease"])
    anyio.run(parts.keys.remove, "cursor")
    d = make_dispatcher(parts)
    # The transport answers HTTP 401 LEASE_UNKNOWN. The bridge then renews the
    # lease and gets BAD_KEY. A NO_FREE_SESSION result would keep the bridge on
    # a lease that no call can use.
    with pytest.raises(LeaseRequired):
        tool(d, token, "whoami")
    # The call ended the lease.
    with pytest.raises(LeaseRequired):
        tool(d, token, "login", {"session": "lane-a"})


def test_a_bridge_tool_on_a_lease_with_no_session_binds_no_session(tmp_path):
    parts, pool, token = full_pool(tmp_path)
    anyio.run(parts.leases.end, pool[0]["lease"])
    d = make_dispatcher(parts)
    for name, args in (
        ("login", {"session": "lane-a"}),
        ("logout", {"session": "ses-abcdefghijklmnop"}),
    ):
        result = tool(d, token, name, args)[1]["result"]
        assert result["structuredContent"]["error"]["code"] == "BRIDGE_ONLY"
    assert beat(parts, token) == {"session": None, "new_mail": 0}


def test_no_free_session_over_post_mcp_is_http_200_not_401(tmp_path):
    parts, _pool, token = full_pool(tmp_path)

    @Module(
        controllers=[McpController],
        providers=[
            ValueProvider(provide=Dispatcher, use_value=make_dispatcher(parts)),
            ValueProvider(
                provide=McpHttpPolicy,
                use_value=McpHttpPolicy(frozenset({HOST}), frozenset()),
            ),
            ValueProvider(provide=WaitLane, use_value=WaitLane()),
        ],
    )
    class PoolMcpModule:
        pass

    call = {
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {"name": "whoami", "arguments": {}},
    }
    with AsgiTestClient(create_test_app(PoolMcpModule)) as client:
        answer = client.post(
            "/mcp",
            content=json.dumps(call),
            headers={
                "host": HOST,
                "content-type": "application/json",
                "authorization": f"Bearer {token}",
            },
        )
        assert answer.status_code == 200
        assert answer.json()["result"]["structuredContent"] == NO_FREE_SESSION
        unknown = client.post(
            "/mcp",
            content=json.dumps(call),
            headers={
                "host": HOST,
                "content-type": "application/json",
                "authorization": "Bearer lease_" + "a" * 43,
            },
        )
        assert unknown.status_code == 401
        assert unknown.json()["error"]["code"] == "LEASE_UNKNOWN"
