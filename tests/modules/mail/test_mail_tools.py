"""Unit tests of the mail tools with a memory mail server, the memory adapters and fake sessions and claims."""

import email
import email.policy
import itertools
import sys
import types
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, cast

import anyio
import pytest
from bustan import Module

from mai.platform.contract import Ctx, ToolError

if TYPE_CHECKING:
    from mai.modules.sessions import SessionService
    from mai.modules.threads import ClaimService


def _ensure(name, **attrs):
    # The sibling modules can be absent while the tree is in change; a stub keeps these unit tests independent.
    try:
        module = __import__(name, fromlist=list(attrs))
        if all(hasattr(module, key) for key in attrs):
            return
    except Exception:  # noqa: BLE001, S110
        pass
    stub = types.ModuleType(name)
    for key, value in attrs.items():
        setattr(stub, key, value)
    sys.modules[name] = stub


_ensure(
    "mai.modules.sessions",
    SessionService=type("SessionService", (), {}),
    SessionsModule=Module()(type("SessionsModule", (), {})),
)
_ensure(
    "mai.modules.threads",
    ClaimService=type("ClaimService", (), {}),
    ThreadsModule=Module()(type("ThreadsModule", (), {})),
)

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
from mai.modules.mcp.application.json_rpc import validate
from mai.modules.mcp.domain.tool_schemas import BY_NAME


class FakeMail:
    def __init__(self):
        self.boxes = {"INBOX": {}, "Sent": {}, "Done": {}, "shared/cursor/INBOX": {}}
        self.uid = itertools.count(1)
        self.sent = []
        self.header_fetches = []
        # The number of the next sends that fail, as when Postfix does not reply.
        self.failed_sends = 0

    def add(self, box, raw, flags=()):
        uid = next(self.uid)
        self.boxes[box][uid] = {"raw": raw, "flags": set(flags)}
        return uid

    def send(self, raw, mail_from, rcpt_to):
        if self.failed_sends:
            self.failed_sends -= 1
            raise ToolError("MAIL_SERVER_DOWN")
        self.sent.append((raw, mail_from, list(rcpt_to)))

    def append(self, mailbox, raw, flags=()):
        self.add(mailbox, raw, flags)

    def search(self, mailbox, criteria):
        out = []
        for uid, m in self.boxes[mailbox].items():
            if "UNSEEN" in criteria and "\\Seen" in m["flags"]:
                continue
            if "UNKEYWORD $MaiNotified" in criteria and "$MaiNotified" in m["flags"]:
                continue
            out.append(uid)
        return out

    def fetch_headers(self, mailbox, uids):
        self.header_fetches.append(mailbox)
        out = []
        for uid in uids:
            m = self.boxes[mailbox][uid]
            msg = email.message_from_bytes(m["raw"], policy=email.policy.default)
            out.append(
                {
                    "uid": uid,
                    "flags": sorted(m["flags"]),
                    "size": len(m["raw"]),
                    "headers": {k.lower(): str(v) for k, v in msg.items()},
                }
            )
        return out

    def fetch_raw(self, mailbox, uid):
        return self.boxes[mailbox][uid]["raw"]

    def store_flags(self, mailbox, uids, add=(), remove=()):
        for uid in uids:
            self.boxes[mailbox][uid]["flags"] |= set(add)
            self.boxes[mailbox][uid]["flags"] -= set(remove)

    def move(self, mailbox, uid, dest):
        m = self.boxes[mailbox].pop(uid)
        self.add(dest, m["raw"], m["flags"])
        return True

    def idle(self, mailboxes, timeout_sec):
        return False


@dataclass
class State:
    login: str
    agent: str
    handle: str
    mail: object
    kind: str = "session"
    roles: dict = field(default_factory=dict)

    @property
    def address(self):
        return f"{self.login}@mai.internal"


class FakeSessions:
    def __init__(self, state):
        self.state = state

    async def resolve(self, handle):
        if handle != self.state.handle:
            raise ToolError("SESSION_UNKNOWN", path="session")
        return self.state


class FakeClaims:
    def __init__(self):
        self.calls = []
        self.refuse = False

    async def claim(self, state, ref=None, thread_ref=None):
        self.calls.append(thread_ref)
        if self.refuse:
            raise ToolError("THREAD_CLAIMED", owner="cursor.lane-b@mai.internal")
        return {"thread_ref": thread_ref}

    async def holder(self, agent, root_msg_id):
        return "cursor.lane-a@mai.internal" if self.calls else None


def make_env(sent):
    fake = FakeMail()
    state = State("cursor.lane-a", "cursor", "ses-test", fake)
    claims = FakeClaims()
    # The fakes give the same calls as the real services; the casts state that.
    claim_service = cast("ClaimService", claims)
    directory = InMemoryDirectory()
    directory.add_agent("codex")
    directory.add_session("cursor", "cursor.lane-a", "lane-a")
    reader = MailReader(
        cast("SessionService", FakeSessions(state)), ThreadedMailboxAccess()
    )
    sender = MailSender(reader, sent, directory)
    tools = MailTools(
        ListAddressesHandler(reader, directory),
        SendMessageHandler(reader, sender),
        ReplyHandler(reader, sender, claim_service),
        ListInboxHandler(reader),
        ReadMessageHandler(reader),
        ReadThreadHandler(reader, claim_service),
        WaitForMailHandler(reader, WaitSlots()),
        MarkDoneHandler(reader),
    ).tools()
    ctx = Ctx(state, "stdio")

    def call(name, args, context=ctx):
        return anyio.run(tools[name], context, args)

    return fake, claims, call


@pytest.fixture
def env():
    return make_env(InMemorySentRequests())


def make_mail(
    mid,
    frm="codex@mai.internal",
    to="cursor@mai.internal",
    subject="Hello",
    refs=None,
):
    msg = email.message.EmailMessage()
    msg["From"] = frm
    msg["To"] = to
    msg["Subject"] = subject
    msg["Date"] = "Thu, 12 Nov 2026 09:00:00 +0000"
    msg["Message-ID"] = mid
    msg["X-Mai-Kind"] = "request"
    msg["X-Mai-Priority"] = "high"
    if refs:
        msg["In-Reply-To"] = refs[-1]
        msg["References"] = " ".join(refs)
    msg.set_content("text")
    return msg.as_bytes()


def test_session_errors(env):
    _, _, call = env
    with pytest.raises(ToolError) as e:
        call("list_inbox", {}, Ctx(None, "stdio"))
    assert e.value.code == "NOT_LOGGED_IN"
    with pytest.raises(ToolError) as e:
        call("list_inbox", {}, Ctx(None, "http"))
    assert e.value.code == "SESSION_UNKNOWN"


def test_list_read_done(env):
    fake, _, call = env
    fake.add("shared/cursor/INBOX", make_mail("<m1@mai.internal>"))
    fake.add("INBOX", make_mail("<m2@mai.internal>", to="cursor.lane-a@mai.internal"))
    listed = call("list_inbox", {"unread_only": True})
    assert len(listed["messages"]) == 2 and "uid" not in str(listed).lower()
    queued = next(m for m in listed["messages"] if m["in_queue"])
    assert queued["ref"] == r.ref_of("<m1@mai.internal>")
    assert (
        queued["status"] == "unread"
        and queued["kind"] == "request"
        and queued["priority"] == "high"
    )
    read = call("read_message", {"ref": queued["ref"]})
    assert read["notice"] == r.NOTICE and read["message"]["body"].strip() == "text"
    assert read["message"]["status"] == "seen"
    assert call("mark_done", {"ref": queued["ref"]})["status"] == "done"
    assert len(fake.boxes["Done"]) == 1
    assert call("mark_done", {"ref": queued["ref"]})["status"] == "done"
    with pytest.raises(ToolError) as e:
        call("read_message", {"ref": "ref-0000000000000000"})
    assert e.value.code == "UNKNOWN_MESSAGE"
    with pytest.raises(ToolError) as e:
        call("read_message", {"ref": "bad"})
    assert e.value.code == "INVALID_ARGUMENT" and e.value.path == "ref"


def test_ref_lookup_stops_at_first_mailbox(env):
    fake, _, call = env
    fake.add("INBOX", make_mail("<m1@mai.internal>"))
    fake.add("Sent", make_mail("<m9@mai.internal>"))
    fake.header_fetches.clear()
    call("read_message", {"ref": r.ref_of("<m1@mai.internal>")})
    assert fake.header_fetches == ["INBOX"]


def test_send_with_retry(env):
    fake, _, call = env
    args = {"to": ["codex"], "subject": "Hi", "body": "x", "request_id": "r1"}
    first = call("send_message", args)
    again = call("send_message", dict(args))
    assert again["duplicate"] and again["message_id"] == first["message_id"]
    assert len(fake.sent) == 1 and fake.sent[0][1] == "cursor.lane-a@mai.internal"
    assert len(fake.boxes["Sent"]) == 1
    for extra, code in (
        ({"body": "y"}, "RETRY_KEY_REUSED"),
        ({"to": ["x@example.com"], "request_id": "r2"}, "RECIPIENT_UNKNOWN"),
        ({"to": ["nobody"], "request_id": "r3"}, "RECIPIENT_UNKNOWN"),
        ({"subject": "a\nb", "request_id": "r4"}, "REFUSED_HEADER"),
        ({"body": "x" * 70000, "request_id": "r5"}, "BODY_TOO_LARGE"),
    ):
        with pytest.raises(ToolError) as e:
            call("send_message", {**args, **extra})
        assert e.value.code == code


def test_reply_and_thread_without_claim(env):
    fake, claims, call = env
    fake.add("INBOX", make_mail("<root@mai.internal>", to="cursor.lane-a@mai.internal"))
    ref = r.ref_of("<root@mai.internal>")
    out = call(
        "reply", {"ref": ref, "body": "answer", "request_id": "q1", "reply_all": True}
    )
    msg = email.message_from_bytes(fake.sent[0][0], policy=email.policy.default)
    assert (
        msg["In-Reply-To"] == "<root@mai.internal>"
        and msg["References"] == "<root@mai.internal>"
    )
    assert msg["Subject"] == "Re: Hello" and fake.sent[0][2] == ["codex@mai.internal"]
    assert out["thread_ref"] == r.thread_ref_of("<root@mai.internal>")
    assert claims.calls == []
    thread = call("read_thread", {"ref": ref})
    assert len(thread["messages"]) == 2 and thread["thread_ref"] == out["thread_ref"]
    assert thread["owner"] is None


def test_reply_to_queue_thread_claims_first(env):
    fake, claims, call = env
    fake.add("shared/cursor/INBOX", make_mail("<q@mai.internal>"))
    ref = r.ref_of("<q@mai.internal>")
    call("reply", {"ref": ref, "body": "mine", "request_id": "c1"})
    assert claims.calls == [r.thread_ref_of("<q@mai.internal>")]
    assert call("read_thread", {"thread_ref": r.thread_ref_of("<q@mai.internal>")})[
        "owner"
    ]
    claims.refuse = True
    with pytest.raises(ToolError) as e:
        call("reply", {"ref": ref, "body": "again", "request_id": "c2"})
    assert e.value.code == "THREAD_CLAIMED"
    assert len(fake.sent) == 1


def test_wait_for_mail(env):
    fake, _, call = env
    assert call("wait_for_mail", {"timeout_sec": 0})["timed_out"] is True
    fake.add("shared/cursor/INBOX", make_mail("<w1@mai.internal>"))
    got = call("wait_for_mail", {"timeout_sec": 1})
    assert not got["timed_out"] and got["messages"][0]["status"] == "notified"
    assert call("wait_for_mail", {"timeout_sec": 0})["timed_out"] is True
    with pytest.raises(ToolError):
        call("wait_for_mail", {"timeout_sec": 51})


def test_list_addresses(env):
    _, _, call = env
    out = call("list_addresses", {})
    assert out["me"] == "cursor.lane-a@mai.internal"
    cursor = next(a for a in out["agents"] if a["address"] == "cursor@mai.internal")
    assert cursor["sessions"][0]["label"] == "lane-a"
    assert [a["address"] for a in out["agents"]] == [
        "codex@mai.internal",
        "cursor@mai.internal",
    ]


def test_migrations_are_in_range():
    from mai.modules.mail.infra import MIGRATIONS

    assert all(400 <= m.version < 500 for m in MIGRATIONS)


SEND_OUTPUT = BY_NAME["send_message"]["outputSchema"]


class NoDeleteSentRequests(InMemorySentRequests):
    """The memory store, with a release that fails as a DELETE of the app role does in PostgreSQL."""

    async def release(self, login, request_id, message_id):
        raise PermissionError("permission denied for table sent_request")


def test_a_replay_gives_the_date_of_the_sent_mail(env):
    _, _, call = env
    args = {"to": ["codex"], "subject": "Hi", "body": "x", "request_id": "d1"}
    first = call("send_message", args)
    again = call("send_message", dict(args))
    assert again["duplicate"] and again["message_id"] == first["message_id"]
    assert again["date"] == first["date"]
    validate(SEND_OUTPUT, first)
    validate(SEND_OUTPUT, again)


@pytest.mark.parametrize(
    "sent", [InMemorySentRequests, NoDeleteSentRequests], ids=["release", "no-delete"]
)
def test_a_retry_after_a_failed_send_delivers_the_mail_once(sent):
    fake, _, call = make_env(sent())
    args = {"to": ["codex"], "subject": "Hi", "body": "x", "request_id": "f1"}
    fake.failed_sends = 1
    with pytest.raises(ToolError) as e:
        call("send_message", args)
    # The error of the send is the result, also when the store of the keys fails.
    assert e.value.code == "MAIL_SERVER_DOWN"
    assert fake.sent == [] and fake.boxes["Sent"] == {}
    retry = call("send_message", dict(args))
    assert retry["duplicate"] is False
    validate(SEND_OUTPUT, retry)
    again = call("send_message", dict(args))
    assert again["duplicate"] and again["message_id"] == retry["message_id"]
    assert len(fake.sent) == 1 and len(fake.boxes["Sent"]) == 1
