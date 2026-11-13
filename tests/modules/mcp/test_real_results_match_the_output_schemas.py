"""The real results of the server tools match the output schemas of tools/list.

Each call goes through the real Dispatcher, the real lease service and session
service on the memory stores, the real mail, threads, sessions, and board tool
code. Only the mail server is a fake (the fake of the mail tool tests). Each
structured content is checked with jsonschema Draft 7 and Draft 2020-12.
"""

import email.message
import smtplib
from typing import cast

import anyio
import pytest
from jsonschema import Draft7Validator, Draft202012Validator

from mai.modules.board import BoardTools
from mai.modules.board.infra.memory_board_store import MemoryBoardStore
from mai.modules.mail.api.mail_tools import MailTools
from mai.modules.mail.application.list_addresses_query import ListAddressesHandler
from mai.modules.mail.application.list_inbox_query import ListInboxHandler
from mai.modules.mail.application.mail_reader import MailReader
from mai.modules.mail.application.mail_sender import MailSender
from mai.modules.mail.application.mark_done_command import MarkDoneHandler
from mai.modules.mail.application.read_message_query import ReadMessageHandler
from mai.modules.mail.application.read_thread_query import ReadThreadHandler
from mai.modules.mail.application.reply_command import ReplyHandler
from mai.modules.mail.application.send_message_command import SendMessageHandler
from mai.modules.mail.application.wait_for_mail_query import (
    WaitForMailHandler,
    WaitSlots,
)
from mai.modules.mail.domain import mail_rules as r
from mai.modules.mail.infra.memory_directory import InMemoryDirectory
from mai.modules.mail.infra.memory_sent_requests import InMemorySentRequests
from mai.modules.mail.infra.threaded_mailbox_access import ThreadedMailboxAccess
from mai.modules.mcp import Dispatcher, McpConnection
from mai.modules.mcp.domain.tool_schemas import TOOLS
from mai.modules.sessions import SessionTools
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from mai.modules.threads import ClaimService, ThreadTools
from mai.modules.threads.infra.in_memory_claim_store import InMemoryClaimStore
from mai.platform.mailserver.mail_session import MailSession
from tests.modules.mail.test_mail_tools import FakeMail, make_mail
from tests.modules.sessions.test_lease_service import make_parts
from tests.modules.threads.fakes import RecordingReturner

VALIDATORS = (Draft7Validator, Draft202012Validator)
QUEUE = "shared/cursor/INBOX"


class Mail(FakeMail):
    """The fake mail server of the mail tool tests, with a shared agent queue,
    an optional real MailSession for SMTP, and an optional failure of fetch_raw."""

    def __init__(self, other=None):
        super().__init__()
        if other is not None:
            self.boxes[QUEUE] = other.boxes[QUEUE]
            self.uid = other.uid
        self.smtp = None
        self.raw_error = None

    def send(self, raw, mail_from, rcpt_to):
        if self.smtp is not None:
            return self.smtp.send(raw, mail_from, rcpt_to)
        return super().send(raw, mail_from, rcpt_to)

    def fetch_raw(self, mailbox, uid):
        if self.raw_error is not None:
            raise self.raw_error
        return super().fetch_raw(mailbox, uid)

    def close(self):
        pass


class RefusingSmtp:
    """An SMTP connection where Postfix refuses each recipient with 550."""

    def sendmail(self, mail_from, rcpt_to, raw):
        raise smtplib.SMTPRecipientsRefused(
            {t: (550, b"5.1.1 Recipient address rejected") for t in rcpt_to}
        )


class World:
    def __init__(self, tmp_path):
        self.parts = parts = make_parts(tmp_path)
        self.a = anyio.run(parts.leases.acquire, parts.key, "it", "/a", 1)["lease"]
        self.b = anyio.run(parts.leases.acquire, parts.key, "it", "/b", 2)["lease"]
        self.state_a = anyio.run(parts.leases.state, self.a)
        self.state_b = anyio.run(parts.leases.state, self.b)
        assert (self.state_a.login, self.state_b.login) == ("cursor.s1", "cursor.s2")
        self.mail_a = Mail()
        self.mail_b = Mail(self.mail_a)
        self.state_a.mail = self.mail_a
        self.state_b.mail = self.mail_b
        # The worker role lets the board tools pass the role check of the dispatcher.
        self.state_b.roles = {"api": "worker"}
        directory = InMemoryDirectory()
        directory.add_agent("codex")
        directory.add_session("cursor", "cursor.s1", "s1")
        directory.add_session("cursor", "cursor.s2", "s2")
        claims = ClaimService(InMemoryClaimStore(), RecordingReturner())
        reader = MailReader(parts.sessions, ThreadedMailboxAccess())
        sender = MailSender(reader, InMemorySentRequests(), directory)
        self.slots = WaitSlots()
        registry = {}
        registry.update(SessionTools(parts.sessions).tools())
        registry.update(
            MailTools(
                ListAddressesHandler(reader, directory),
                SendMessageHandler(reader, sender),
                ReplyHandler(reader, sender, claims),
                ListInboxHandler(reader),
                ReadMessageHandler(reader),
                ReadThreadHandler(reader, claims),
                WaitForMailHandler(reader, self.slots),
                MarkDoneHandler(reader),
            ).tools()
        )
        registry.update(ThreadTools(claims, parts.sessions).tools())
        registry.update(BoardTools(MemoryBoardStore(), parts.sessions).tools())
        assert set(registry) == {t["name"] for t in TOOLS} - {"login", "logout"}
        self.d = Dispatcher(
            registry, parts.sessions.resolve, parts.leases.state, parts.leases.check
        )
        self.results = []

    def send(self, msg, conn=None, transport="http", token=None):
        headers = {"Authorization": f"Bearer {token}"} if token else None
        return anyio.run(self.d.handle, msg, conn, transport, headers)

    def tools_list(self):
        status, body = self.send(
            {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, token=self.a
        )
        assert status == 200
        return {t["name"]: t for t in body["result"]["tools"]}

    def call(self, token, name, args=None, *, conn=None, transport="http", raw=False):
        arguments = args if raw else (args or {})
        status, body = self.send(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/call",
                "params": {"name": name, "arguments": arguments},
            },
            conn,
            transport,
            token,
        )
        assert status == 200, body
        result = body["result"]
        self.results.append((name, args, result))
        return result


def code(result):
    return (result["structuredContent"].get("error") or {}).get("code")


def scenario(w):
    """Each tool on success and on each error path that the fakes reach."""
    a, b = w.a, w.b
    w.mail_a.add(QUEUE, make_mail("<q1@mai.internal>"))
    w.mail_a.add(QUEUE, make_mail("<q2@mai.internal>"))
    w.mail_a.add(QUEUE, make_mail("<q3@mai.internal>"))
    w.mail_a.add("INBOX", make_mail("<i1@mai.internal>", to="cursor.s1@mai.internal"))
    q1, q2, q3 = (r.ref_of(f"<q{i}@mai.internal>") for i in (1, 2, 3))
    i1 = r.ref_of("<i1@mai.internal>")
    t2, t3 = r.thread_ref_of("<q2@mai.internal>"), r.thread_ref_of("<q3@mai.internal>")
    unknown_ref, unknown_thr = "ref-0000000000000000", "thr-0000000000000000"

    # Success results.
    assert code(w.call(a, "whoami")) is None
    assert code(w.call(b, "whoami")) is None
    assert code(w.call(a, "list_addresses")) is None
    assert len(w.call(a, "list_inbox")["structuredContent"]["messages"]) == 4
    assert code(w.call(a, "list_inbox", {"unread_only": True, "limit": 2})) is None
    assert code(w.call(a, "read_message", {"ref": q1})) is None
    assert code(w.call(a, "read_thread", {"ref": i1})) is None
    assert code(w.call(a, "read_thread", {"thread_ref": t2, "limit": 5})) is None
    got = w.call(a, "wait_for_mail", {"timeout_sec": 0})["structuredContent"]
    assert got["messages"] and not got["timed_out"]
    assert w.call(a, "wait_for_mail", {"timeout_sec": 0})["structuredContent"][
        "timed_out"
    ]
    send = {"to": ["codex"], "subject": "Hi", "body": "x", "request_id": "r1"}
    assert code(w.call(a, "send_message", send)) is None
    assert w.call(a, "send_message", dict(send))["structuredContent"]["duplicate"]
    assert (
        code(w.call(a, "reply", {"ref": i1, "body": "ok", "request_id": "p1"})) is None
    )
    # A reply to a queue mail claims its thread first, with the real claim service.
    assert (
        code(w.call(a, "reply", {"ref": q1, "body": "ok", "request_id": "p2"})) is None
    )
    assert code(w.call(a, "claim_thread", {"ref": q2})) is None
    rel = w.call(a, "release_thread", {"thread_ref": t2})["structuredContent"]
    assert rel["released"] is True
    rel = w.call(a, "release_thread", {"thread_ref": t2})["structuredContent"]
    assert rel["released"] is False
    assert code(w.call(a, "claim_thread", {"thread_ref": t3})) is None
    assert code(w.call(a, "mark_done", {"ref": i1})) is None
    assert code(w.call(a, "mark_done", {"ref": i1})) is None

    # THREAD_CLAIMED: lane B on the thread that lane A holds.
    assert code(w.call(b, "claim_thread", {"thread_ref": t3})) == "THREAD_CLAIMED"
    assert code(w.call(b, "claim_thread", {"ref": q3})) == "THREAD_CLAIMED"
    assert code(w.call(b, "release_thread", {"thread_ref": t3})) == "THREAD_CLAIMED"
    w.mail_a.add(
        QUEUE,
        make_mail("<q3b@mai.internal>", refs=["<q3@mai.internal>"]),
    )
    q3b = r.ref_of("<q3b@mai.internal>")
    out = w.call(b, "reply", {"ref": q3b, "body": "mine", "request_id": "p3"})
    assert code(out) == "THREAD_CLAIMED", out

    # UNKNOWN_MESSAGE.
    for name, args in (
        ("read_message", {"ref": unknown_ref}),
        ("mark_done", {"ref": unknown_ref}),
        ("reply", {"ref": unknown_ref, "body": "x", "request_id": "u1"}),
        ("read_thread", {"thread_ref": unknown_thr}),
        ("read_thread", {"ref": unknown_ref}),
        ("claim_thread", {"ref": unknown_ref}),
        ("claim_thread", {"thread_ref": unknown_thr}),
        ("release_thread", {"thread_ref": unknown_thr}),
    ):
        assert code(w.call(a, name, args)) == "UNKNOWN_MESSAGE", name

    # INVALID_ARGUMENT, from the input schema, the one-of check, and the tools.
    for name, args in (
        ("read_message", {"ref": "bad"}),
        ("read_message", {}),
        ("read_thread", {"ref": q1, "thread_ref": t2}),
        ("read_thread", {}),
        ("claim_thread", {}),
        ("wait_for_mail", {"timeout_sec": 51}),
        ("send_message", {**send, "to": ["x@example.com"], "request_id": "e1"}),
        ("list_inbox", {"limit": 0}),
        ("whoami", {"extra": 1}),
        ("get_board", {"project": ""}),
    ):
        assert code(w.call(a, name, args)) == "INVALID_ARGUMENT", name
    assert code(w.call(a, "read_message", "x", raw=True)) == "INVALID_ARGUMENT"
    assert code(w.call(a, "release_thread", {"thread_ref": t2, "ref": q1})) in (
        "INVALID_ARGUMENT",
    )

    # The send errors.
    for extra, expected in (
        ({"body": "y"}, "RETRY_KEY_REUSED"),
        ({"to": ["nobody"], "request_id": "s3"}, "RECIPIENT_UNKNOWN"),
        ({"subject": "a\nb", "request_id": "s4"}, "REFUSED_HEADER"),
        ({"body": "é" * 40000, "request_id": "s5"}, "BODY_TOO_LARGE"),
    ):
        assert code(w.call(a, "send_message", {**send, **extra})) == expected
    w.mail_a.failed_sends = 1
    assert code(w.call(a, "send_message", {**send, "request_id": "s6"})) == (
        "MAIL_SERVER_DOWN"
    )
    # Postfix refuses the recipients: the real MailSession gives the field refused.
    smtp = MailSession("cursor.s1", "not-a-secret")
    smtp._smtp = cast("smtplib.SMTP", RefusingSmtp())
    w.mail_a.smtp = smtp
    out = w.call(a, "send_message", {**send, "request_id": "s7"})
    assert code(out) == "RECIPIENT_UNKNOWN"
    assert out["structuredContent"]["refused"] == ["codex@mai.internal"]
    out = w.call(a, "reply", {"ref": q1, "body": "x", "request_id": "s8"})
    assert code(out) == "RECIPIENT_UNKNOWN", out
    w.mail_a.smtp = None

    # INTERNAL: a defect of the mail code, BUSY: no free wait slot.
    w.mail_a.raw_error = RuntimeError("secret detail")
    assert code(w.call(a, "read_message", {"ref": q1})) == "INTERNAL"
    w.mail_a.raw_error = None
    w.slots._total = r.WAIT_TOTAL
    assert code(w.call(a, "wait_for_mail", {"timeout_sec": 0})) == "BUSY"
    w.slots._total = 0

    # BRIDGE_ONLY, FORBIDDEN_ROLE, STORE_UNAVAILABLE.
    assert code(w.call(a, "login", {"session": "lane-a"})) == "BRIDGE_ONLY"
    assert code(w.call(a, "logout", {"session": "ses-" + "a" * 16})) == "BRIDGE_ONLY"
    assert code(w.call(a, "get_board", {"project": "api"})) == "FORBIDDEN_ROLE"
    assert (
        code(
            w.call(b, "plan_item", {"project": "api", "title": "t", "request_id": "b1"})
        )
        == "FORBIDDEN_ROLE"
    )
    for name, args in (
        ("assign_item", {"item": "itm-aaaaaaaaaaaa", "request_id": "b5"}),
        (
            "review_item",
            {"item": "itm-aaaaaaaaaaaa", "decision": "accept", "request_id": "b6"},
        ),
        ("manage_sprint", {"project": "api", "action": "open", "request_id": "b7"}),
    ):
        assert code(w.call(b, name, args)) == "FORBIDDEN_ROLE", name
    for name, args in (
        ("get_board", {"project": "api"}),
        ("read_item", {"item": "itm-aaaaaaaaaaaa"}),
        ("start_item", {"item": "itm-aaaaaaaaaaaa", "request_id": "b2"}),
        (
            "report_status",
            {"item": "itm-aaaaaaaaaaaa", "summary": "s", "request_id": "b3"},
        ),
        (
            "submit_for_review",
            {"item": "itm-aaaaaaaaaaaa", "evidence": {}, "request_id": "b4"},
        ),
    ):
        assert code(w.call(b, name, args)) == "STORE_UNAVAILABLE", name

    # stdio: NOT_LOGGED_IN, SESSION_UNKNOWN, and whoami with no session.
    conn = McpConnection()
    assert code(w.call(None, "whoami", conn=conn, transport="stdio")) is None
    assert (
        code(w.call(None, "list_inbox", conn=conn, transport="stdio"))
        == "NOT_LOGGED_IN"
    )
    assert (
        code(w.call(None, "get_board", conn=conn, transport="stdio")) == "NOT_LOGGED_IN"
    )
    lost = McpConnection(handle="ses-" + "z" * 26)
    assert (
        code(w.call(None, "list_inbox", conn=lost, transport="stdio"))
        == "SESSION_UNKNOWN"
    )
    lost = McpConnection(handle="ses-" + "z" * 26)
    assert code(w.call(None, "whoami", conn=lost, transport="stdio")) is None

    # NO_FREE_SESSION: a lease with no session when the 8 pool sessions have leases.
    parts = w.parts
    for pid in range(3, MAX_SESSIONS + 1):
        anyio.run(parts.leases.acquire, parts.key, "it", "/p", pid)
    extra = anyio.run(parts.leases.acquire, parts.key, "it", "/p", 99)
    assert extra["session"] is None
    for name, args in (
        ("list_inbox", {}),
        ("whoami", {}),
        ("send_message", send),
        ("claim_thread", {"ref": q1}),
        ("get_board", {}),
    ):
        assert code(w.call(extra["lease"], name, args)) == "NO_FREE_SESSION", name
    assert code(w.call(extra["lease"], "login", {"session": "lane-a"})) == "BRIDGE_ONLY"


def problems(validator, schema, value):
    return sorted(e.message for e in validator(schema).iter_errors(value))


@pytest.fixture
def world(tmp_path):
    w = World(tmp_path)
    scenario(w)
    return w


def test_each_output_schema_of_tools_list_is_a_valid_object_schema(tmp_path):
    listed = World(tmp_path).tools_list()
    assert len(listed) == 22
    for name, t in listed.items():
        schema = t["outputSchema"]
        assert schema.get("type") == "object", name
        for validator in VALIDATORS:
            validator.check_schema(schema)


@pytest.mark.parametrize("validator", VALIDATORS, ids=lambda v: v.__name__)
def test_each_real_result_matches_the_output_schema_of_tools_list(world, validator):
    listed = world.tools_list()
    failures = []
    for name, args, result in world.results:
        structured = result["structuredContent"]
        if bool(result.get("isError")) != ("error" in structured):
            failures.append((name, args, "isError", result.get("isError")))
        found = problems(validator, listed[name]["outputSchema"], structured)
        if found:
            failures.append((name, args, code(result), found))
    assert failures == []


def test_the_scenario_reaches_each_tool_and_the_error_codes(world):
    names = {name for name, _, _ in world.results}
    assert names == {t["name"] for t in TOOLS}
    codes = {code(res) for _, _, res in world.results} - {None}
    assert codes >= {
        "UNKNOWN_MESSAGE",
        "INVALID_ARGUMENT",
        "RECIPIENT_UNKNOWN",
        "THREAD_CLAIMED",
        "NO_FREE_SESSION",
        "BRIDGE_ONLY",
        "FORBIDDEN_ROLE",
        "STORE_UNAVAILABLE",
        "NOT_LOGGED_IN",
        "SESSION_UNKNOWN",
        "RETRY_KEY_REUSED",
        "REFUSED_HEADER",
        "BODY_TOO_LARGE",
        "MAIL_SERVER_DOWN",
        "INTERNAL",
        "BUSY",
    }, sorted(codes)
    succeeded = {name for name, _, res in world.results if code(res) is None}
    # The board tools need PostgreSQL for a success result: the container test has them.
    assert succeeded >= {
        "whoami",
        "list_addresses",
        "send_message",
        "reply",
        "list_inbox",
        "read_message",
        "read_thread",
        "wait_for_mail",
        "mark_done",
        "claim_thread",
        "release_thread",
    }


@pytest.mark.parametrize("validator", VALIDATORS, ids=lambda v: v.__name__)
def test_a_real_success_result_with_no_required_field_does_not_match(world, validator):
    listed = world.tools_list()
    checked = 0
    for name, _, result in world.results:
        structured = result["structuredContent"]
        schema = listed[name]["outputSchema"]
        if code(result) is not None or "anyOf" not in schema:
            continue
        for field in schema["anyOf"][0]["required"]:
            broken = {k: v for k, v in structured.items() if k != field}
            assert not validator(schema).is_valid(broken), (name, field)
            checked += 1
    assert checked > 0


def _raw(mid, date=True, sender=True):
    msg = email.message.EmailMessage()
    if sender:
        msg["From"] = "codex@mai.internal"
    msg["To"] = "cursor.s1@mai.internal"
    msg["Subject"] = "Hello"
    if date:
        msg["Date"] = (
            date if isinstance(date, str) else "Thu, 12 Nov 2026 09:00:00 +0000"
        )
    msg["Message-ID"] = mid
    msg.set_content("text")
    return msg.as_bytes()


@pytest.mark.parametrize(
    "raw",
    [
        _raw("<nodate@mai.internal>", date=False),
        _raw("<baddate@mai.internal>", date="not a date"),
        _raw("<nofrom@mai.internal>", sender=False),
    ],
    ids=["no-date", "bad-date", "no-from"],
)
@pytest.mark.parametrize("validator", VALIDATORS, ids=lambda v: v.__name__)
def test_a_mail_with_no_date_or_no_from_gives_results_that_match(
    tmp_path, raw, validator
):
    w = World(tmp_path)
    w.mail_a.add("INBOX", raw)
    ref = r.ref_of(email.message_from_bytes(raw)["Message-ID"])
    listed = w.tools_list()
    failures = []
    for name, args in (
        ("list_inbox", {}),
        ("read_message", {"ref": ref}),
        ("read_thread", {"ref": ref}),
        ("wait_for_mail", {"timeout_sec": 0}),
    ):
        result = w.call(w.a, name, args)
        assert code(result) is None, result
        found = problems(
            validator, listed[name]["outputSchema"], result["structuredContent"]
        )
        if found:
            failures.append((name, found))
    assert failures == []


@pytest.mark.parametrize(
    "raw",
    [
        _raw("<nodate2@mai.internal>", date=False),
        _raw("<nofrom2@mai.internal>", sender=False),
    ],
    ids=["no-date", "no-from"],
)
def test_wait_for_mail_first_with_a_mail_with_no_date_or_no_from(tmp_path, raw):
    w = World(tmp_path)
    w.mail_a.add("INBOX", raw)
    listed = w.tools_list()
    result = w.call(w.a, "wait_for_mail", {"timeout_sec": 0})
    assert result["structuredContent"]["messages"], result
    found = problems(
        Draft7Validator,
        listed["wait_for_mail"]["outputSchema"],
        result["structuredContent"],
    )
    assert found == []
