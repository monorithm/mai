"""The dispatcher with a fake registry and a fake session resolver."""

import json

import anyio

from mai.modules.mcp import Dispatcher, McpConnection
from mai.platform.contract import MESSAGES

from ..fakes import HANDLE, SUPER, fake_registry, resolver


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
            "version": "2.0.0",
        }
        assert (
            r["capabilities"] == {"tools": {"listChanged": True}}
            and "login" in r["instructions"]
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


def test_stdio_list_before_and_after_login_and_notification():
    sent = []
    d = Dispatcher(fake_registry(), resolver)
    conn = McpConnection(notify=sent.append)
    assert names(d, conn, "stdio") == ["login", "whoami"]
    r = call(d, "list_inbox", conn=conn, transport="stdio")
    assert r["isError"] and r["structuredContent"]["error"]["code"] == "NOT_LOGGED_IN"
    r = call(
        d, "login", {"credential": "mai1:cursor.lane-a:x"}, conn=conn, transport="stdio"
    )
    assert (
        not r.get("isError")
        and json.loads(r["content"][0]["text"]) == r["structuredContent"]
    )
    assert sent == [{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"}]
    listed = names(d, conn, "stdio")
    assert (
        "list_inbox" in listed and "start_item" in listed and "plan_item" not in listed
    )
    assert (
        call(d, "list_inbox", conn=conn, transport="stdio")["structuredContent"][
            "login"
        ]
        == "cursor.lane-a"
    )
    r = call(
        d,
        "plan_item",
        {"project": "p", "title": "t", "request_id": "r"},
        conn=conn,
        transport="stdio",
    )
    assert r["structuredContent"]["error"]["code"] == "FORBIDDEN_ROLE"
    call(d, "logout", conn=conn, transport="stdio")
    assert conn.handle is None and len(sent) == 2


def test_login_failure_and_internal_error_hide_text():
    d = Dispatcher(fake_registry(), resolver)
    conn = McpConnection()
    e = call(
        d,
        "login",
        {"credential": "mai1:cursor.lane-a:bad"},
        conn=conn,
        transport="stdio",
    )
    e = e["structuredContent"]["error"]
    assert e == {
        "code": "LOGIN_FAILED",
        "message": MESSAGES["LOGIN_FAILED"],
        "path": None,
        "retry_safe": False,
    }
    call(
        d, "login", {"credential": "mai1:cursor.lane-a:x"}, conn=conn, transport="stdio"
    )
    r = call(d, "mark_done", {"ref": "ref-" + "0" * 16}, conn=conn, transport="stdio")
    assert r["structuredContent"]["error"][
        "code"
    ] == "INTERNAL" and "secret" not in json.dumps(r)


def test_validation():
    d = Dispatcher(fake_registry(), resolver)
    e = call(d, "list_inbox", {"session": HANDLE, "limit": 500})["structuredContent"][
        "error"
    ]
    assert e["code"] == "INVALID_ARGUMENT" and e["path"] == "limit"
    e = call(
        d,
        "send_message",
        {"session": HANDLE, "to": ["cursor"], "subject": "s", "body": "b"},
    )
    assert e["structuredContent"]["error"]["path"] == "request_id"
    assert (
        call(d, "list_inbox", {"session": HANDLE, "bogus": 1})["structuredContent"][
            "error"
        ]["path"]
        == "bogus"
    )
    assert (
        call(d, "login", {})["structuredContent"]["error"]["code"] == "INVALID_ARGUMENT"
    )


def test_http_session_rules():
    d = Dispatcher(fake_registry(), resolver)
    assert len(names(d)) == 22
    assert (
        call(d, "list_inbox")["structuredContent"]["error"]["code"] == "SESSION_UNKNOWN"
    )
    assert (
        call(d, "list_inbox", {}, {"Mai-Session": HANDLE})["structuredContent"]["login"]
        == "cursor.lane-a"
    )
    assert (
        call(d, "list_inbox", {}, {"mai-session": HANDLE})["structuredContent"]["login"]
        == "cursor.lane-a"
    )
    e = call(d, "list_inbox", {"session": HANDLE}, {"Mai-Session": SUPER})[
        "structuredContent"
    ]["error"]
    assert e["code"] == "INVALID_ARGUMENT" and e["path"] == "session"
    plan = {"project": "p", "title": "t", "request_id": "r"}
    assert (
        call(d, "plan_item", dict(plan, session=HANDLE))["structuredContent"]["error"][
            "code"
        ]
        == "FORBIDDEN_ROLE"
    )
    assert (
        call(d, "plan_item", dict(plan, session=SUPER))["structuredContent"]["tool"]
        == "plan_item"
    )
    assert call(d, "whoami")["structuredContent"]["login"] is None


def test_a_tool_call_during_a_restore_gets_mail_server_down_and_runs_nothing(
    monkeypatch,
):
    from mai.platform import supervisor

    d = Dispatcher(fake_registry(), resolver)
    monkeypatch.setattr(supervisor, "held", lambda: {"app", "postfix", "dovecot"})
    result = call(d, "whoami", {"session": HANDLE})
    assert result["isError"] is True
    assert result["structuredContent"]["error"]["code"] == "MAIL_SERVER_DOWN"
    assert result["structuredContent"]["error"]["retry_safe"] is True
