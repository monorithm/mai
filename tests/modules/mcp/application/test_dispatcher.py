"""The dispatcher with a fake registry and a fake session resolver."""

import anyio
import pytest

from mai.modules.mcp import Dispatcher, McpConnection
from mai.modules.mcp.application.dispatcher import LeaseRequired

from ..fakes import HANDLE, SUPER, fake_registry, lease_resolver, resolver


def run(coro_fn, *args):
    return anyio.run(coro_fn, *args)


def call(d, name, args=None, headers=None, conn=None, transport="http"):
    msg = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": {"name": name, "arguments": args or {}},
    }

    async def go():
        return await d.handle(msg, conn, transport, headers)

    return run(go)[1]["result"]


def names(d, conn=None, transport="http"):
    async def go():
        return await d.handle(
            {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, conn, transport
        )

    return [t["name"] for t in run(go)[1]["result"]["tools"]]


def message(d, msg, conn=None, transport="http"):
    async def go():
        return await d.handle(msg, conn, transport)

    return run(go)


def test_initialize_both_revisions():
    d = Dispatcher(fake_registry(), resolver)
    conn = McpConnection()
    for rev in ("2025-11-25", "2026-07-28"):
        r = message(
            d,
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {"protocolVersion": rev},
            },
            conn,
            "stdio",
        )[1]["result"]
        assert r["protocolVersion"] == rev and r["serverInfo"] == {
            "name": "mai",
            "version": "3.2.0",
        }
        assert (
            r["capabilities"]
            == {
                "tools": {"listChanged": True},
                "resources": {"subscribe": False, "listChanged": False},
            }
            and "login" in r["instructions"]
            and "pool session" in r["instructions"]
            and 'session: "lane-1"' in r["instructions"]
        )
    assert (
        message(d, {"jsonrpc": "2.0", "id": 2, "method": "ping"}, conn, "stdio")[1][
            "result"
        ]
        == {}
    )


def test_jsonrpc_envelope_rules():
    d = Dispatcher(fake_registry(), resolver)
    assert message(d, {"jsonrpc": "2.0", "method": "notifications/initialized"}) == (
        202,
        None,
    )
    assert message(d, {"jsonrpc": "1.0", "id": 1, "method": "ping"})[0] == 400
    assert message(d, {"jsonrpc": "2.0", "id": True, "method": "ping"})[0] == 400
    assert (
        message(d, {"jsonrpc": "2.0", "id": 1, "method": "nope"})[1]["error"]["code"]
        == -32601
    )
    meta = {"_meta": {"io.modelcontextprotocol/protocolVersion": "1999-01-01"}}
    assert (
        message(d, {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": meta})[
            1
        ]["error"]["code"]
        == -32022
    )


def lease(handle):
    return {"Authorization": f"Bearer {handle}"}


def test_validation():
    d = Dispatcher(fake_registry(), resolver, lease_resolver)
    e = call(d, "list_inbox", {"limit": 500}, lease(HANDLE))["structuredContent"][
        "error"
    ]
    assert e["code"] == "INVALID_ARGUMENT" and e["path"] == "limit"
    e = call(
        d,
        "send_message",
        {"to": ["cursor"], "subject": "s", "body": "b"},
        lease(HANDLE),
    )
    assert e["structuredContent"]["error"]["path"] == "request_id"
    bogus = call(d, "list_inbox", {"bogus": 1}, lease(HANDLE))
    assert bogus["structuredContent"]["error"]["path"] == "bogus"
    session = call(d, "list_inbox", {"session": HANDLE}, lease(HANDLE))
    assert session["structuredContent"]["error"]["path"] == "session"


def test_http_lease_rules():
    d = Dispatcher(fake_registry(), resolver, lease_resolver)
    assert len(names(d)) == 22
    assert {"login", "logout", "whoami"} <= set(names(d))
    for headers in (
        None,
        {"Authorization": "Bearer lease_unknown"},
        {"Mai-Session": HANDLE},
    ):
        msg = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "whoami", "arguments": {}},
        }

        async def go(headers=headers, msg=msg):
            return await d.handle(msg, None, "http", headers)

        with pytest.raises(LeaseRequired):
            run(go)
    assert (
        call(d, "list_inbox", {}, lease(HANDLE))["structuredContent"]["login"]
        == "cursor.lane-a"
    )
    assert (
        call(d, "list_inbox", {}, {"authorization": f"bearer {HANDLE}"})[
            "structuredContent"
        ]["login"]
        == "cursor.lane-a"
    )
    plan = {"project": "p", "title": "t", "request_id": "r"}
    assert (
        call(d, "plan_item", plan, lease(HANDLE))["structuredContent"]["error"]["code"]
        == "FORBIDDEN_ROLE"
    )
    assert (
        call(d, "plan_item", plan, lease(SUPER))["structuredContent"]["tool"]
        == "plan_item"
    )


def test_a_tool_call_during_a_restore_gets_mail_server_down_and_runs_nothing(
    monkeypatch,
):
    from mai.platform import supervisor

    d = Dispatcher(fake_registry(), resolver, lease_resolver)
    monkeypatch.setattr(supervisor, "held", lambda: {"app", "postfix", "dovecot"})
    result = call(d, "whoami", {}, lease(HANDLE))
    assert result["isError"] is True
    assert result["structuredContent"]["error"]["code"] == "MAIL_SERVER_DOWN"
    assert result["structuredContent"]["error"]["retry_safe"] is True


def test_resources_need_no_login_and_give_the_guide():
    d = Dispatcher(fake_registry(), resolver)

    async def go(method, params=None):
        return await d.handle(
            {"jsonrpc": "2.0", "id": 1, "method": method, "params": params or {}},
            None,
            "http",
        )

    init = run(go, "initialize", {"protocolVersion": "2025-11-25"})[1]["result"]
    assert init["capabilities"]["resources"] == {
        "subscribe": False,
        "listChanged": False,
    }
    assert "mai://guide" in init["instructions"]
    listed = run(go, "resources/list")[1]["result"]["resources"]
    assert [r["uri"] for r in listed] == ["mai://guide"]
    read = run(go, "resources/read", {"uri": "mai://guide"})[1]["result"]
    [content] = read["contents"]
    assert content["mimeType"] == "text/markdown"
    assert content["text"].startswith("# How to use mai")
    # The tool table comes from the schemas: each tool is in the guide.
    for name in ("whoami", "claim_thread", "manage_sprint"):
        assert f"| `{name}` |" in content["text"]
    assert run(go, "resources/templates/list")[1]["result"] == {"resourceTemplates": []}


def test_an_unknown_resource_gives_resource_not_found():
    d = Dispatcher(fake_registry(), resolver)

    async def go(params):
        return await d.handle(
            {"jsonrpc": "2.0", "id": 7, "method": "resources/read", "params": params},
            None,
            "http",
        )

    body = run(go, {"uri": "mai://other"})[1]
    assert body["error"]["code"] == -32002 and body["error"]["data"] == {
        "uri": "mai://other"
    }
    assert run(go, {})[1]["error"]["code"] == -32602


BRIDGE_HANDLE = "ses-" + "abcdefgh" * 2


def test_a_call_with_the_argument_session_that_reaches_the_server_gives_bridge_only():
    seen = []

    async def record(ctx, args):
        seen.append(dict(args))
        return {"login": ctx.session_state.login}

    registry = dict(fake_registry(), list_inbox=record, get_board=record)
    d = Dispatcher(registry, resolver, lease_resolver)
    # Only the bridge reads the argument session. A call with it that comes to the
    # server must not act as the session of its lease, for example the pool session.
    for value in (BRIDGE_HANDLE, "lane-1", "cursor.lane-1", "s1", "ses-" + "a" * 26):
        for name, args in (
            ("list_inbox", {"session": value, "limit": 5}),
            ("get_board", {"session": value, "project": "p"}),
        ):
            result = call(d, name, args, lease(HANDLE))
            assert result["isError"] is True
            error = result["structuredContent"]["error"]
            assert error["code"] == "BRIDGE_ONLY" and error["path"] == "session"
            assert "argument session" in error["message"]
            stdio = call(d, name, args, conn=McpConnection(), transport="stdio")
            assert stdio["structuredContent"]["error"]["code"] == "BRIDGE_ONLY"
    assert seen == []
    # With no argument session, the tool runs as the session of the lease.
    assert call(d, "list_inbox", {"limit": 5}, lease(HANDLE))["structuredContent"] == {
        "login": "cursor.lane-a"
    }
    assert seen == [{"limit": 5}]


def test_a_call_with_the_argument_session_and_no_lease_gives_lease_required():
    d = Dispatcher(fake_registry(), resolver, lease_resolver)

    async def go():
        msg = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "whoami", "arguments": {"session": "lane-1"}},
        }
        return await d.handle(msg, None, "http", None)

    with pytest.raises(LeaseRequired):
        run(go)


def test_a_session_of_no_valid_form_gives_invalid_argument():
    d = Dispatcher(fake_registry(), resolver, lease_resolver)
    for bad in (
        "ses-ABCDEFGHIJKLMNOP",
        "Lane-1",
        "lane-1\n",
        "cursor.lane-1\n",
        "a.b.c",
        "",
        "l" * 33,
        3,
        None,
    ):
        e = call(d, "list_inbox", {"session": bad}, lease(HANDLE))
        assert e["structuredContent"]["error"]["code"] == "INVALID_ARGUMENT", bad
        assert e["structuredContent"]["error"]["path"] == "session", bad


def test_a_pattern_does_not_accept_a_line_break_at_the_end():
    d = Dispatcher(fake_registry(), resolver, lease_resolver)
    for name, args, path in (
        ("mark_done", {"ref": "ref-0123456789abcdef\n"}, "ref"),
        ("read_thread", {"thread_ref": "thr-0123456789abcdef\n"}, "thread_ref"),
        (
            "send_message",
            {"to": ["cursor\n"], "subject": "s", "body": "b", "request_id": "r"},
            "to[0]",
        ),
    ):
        e = call(d, name, args, lease(HANDLE))["structuredContent"]["error"]
        assert e["code"] == "INVALID_ARGUMENT" and e["path"] == path, name


def test_login_and_logout_that_reach_the_server_give_bridge_only():
    d = Dispatcher(fake_registry(), resolver, lease_resolver)
    for name, args in (
        ("login", {"session": "lane-a"}),
        ("logout", {"session": BRIDGE_HANDLE}),
    ):
        result = call(d, name, args, lease(HANDLE))
        assert result["isError"] is True
        assert result["structuredContent"]["error"]["code"] == "BRIDGE_ONLY"
        stdio = call(d, name, args, conn=McpConnection(), transport="stdio")
        assert stdio["structuredContent"]["error"]["code"] == "BRIDGE_ONLY"

        async def go(name=name, args=args):
            msg = {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/call",
                "params": {"name": name, "arguments": args},
            }
            return await d.handle(msg, None, "http", None)

        with pytest.raises(LeaseRequired):
            run(go)
    bad = call(d, "login", {}, lease(HANDLE))
    assert bad["structuredContent"]["error"]["path"] == "session"
