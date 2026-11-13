"""Tests of the message builder and of the IMAP client against a small fake server."""

import imaplib
import os
import socketserver
import threading
import time
from typing import cast

import pytest

from mai.platform.contract import ToolError
from mai.platform.mailserver import MailSession
from mai.platform.mailserver import message as m
from mai.platform.mailserver.mail_session import _expand, _parse_fetch

# Message building and parsing


def _headers(raw: bytes) -> dict:
    return m.parse_headers_bytes(raw.split(b"\r\n\r\n", 1)[0])


def test_build_and_summary_round_trip():
    raw = m.build_message(
        "cursor.lane-a",
        None,
        ["copilot"],
        cc=["owner"],
        subject="Hello",
        body="Body text\n",
        kind="request",
        priority="high",
        expects_reply=True,
        message_id="<msg-1@mai.internal>",
    )
    assert b"\r\n" in raw
    h = _headers(raw)
    assert h["from"] == '"cursor (lane-a)" <cursor.lane-a@mai.internal>'
    s = m.parse_summary(h)
    assert s["ref"] == m.ref_of("<msg-1@mai.internal>")
    assert s["ref"].startswith("ref-") and len(s["ref"]) == 20
    assert s["thread_ref"] == m.thread_ref_of("<msg-1@mai.internal>")
    assert s["thread_ref"].startswith("thr-")
    assert s["from"] == "cursor.lane-a@mai.internal"
    assert s["to"] == ["copilot@mai.internal"]
    assert s["cc"] == ["owner@mai.internal"]
    assert s["kind"] == "request" and s["priority"] == "high"
    assert s["expects_reply"] is True
    assert s["date"].endswith("Z")
    assert s["sender_proved"] is False
    assert m.parse_body(raw).strip() == "Body text"


def test_thread_ref_from_first_reference():
    raw = m.build_message(
        "copilot",
        None,
        "cursor.lane-a",
        subject="Re: Hello",
        body="ok",
        kind="reply",
        in_reply_to="<msg-2@mai.internal>",
        references=["<msg-1@mai.internal>", "<msg-2@mai.internal>"],
    )
    s = m.parse_summary(_headers(raw))
    assert s["thread_ref"] == m.thread_ref_of("<msg-1@mai.internal>")
    assert s["in_reply_to"] == "<msg-2@mai.internal>"


def test_unicode_subject_and_body():
    raw = m.build_message("cursor", None, "owner", subject="Akwaaba été", body="café")
    s = m.parse_summary(_headers(raw))
    assert s["subject"] == "Akwaaba été"
    assert m.parse_body(raw).strip() == "café"


def test_unknown_values_become_unknown():
    s = m.parse_summary(
        {
            "message-id": "<a@x>",
            "from": "a@mai.internal",
            "x-mai-kind": "evil",
            "x-mai-priority": "urgent",
        }
    )
    assert s["kind"] == "unknown" and s["priority"] == "unknown"


@pytest.mark.parametrize("subject", ["a\r\nBcc: x", "tab\there", "nul\x00"])
def test_subject_control_chars_refused(subject):
    with pytest.raises(ToolError) as exc:
        m.build_message("cursor", None, "owner", subject=subject, body="x")
    assert exc.value.code == "REFUSED_HEADER"


def test_subject_and_body_limits():
    m.validate_subject("x" * 200)
    with pytest.raises(ToolError) as exc:
        m.validate_subject("x" * 201)
    assert exc.value.code == "INVALID_ARGUMENT"
    m.validate_body("x" * 65536)
    with pytest.raises(ToolError) as exc:
        m.validate_body("x" * 65537)
    assert exc.value.code == "BODY_TOO_LARGE"


RECEIVED_BOARD = (
    "from localhost (localhost [127.0.0.1]) (Authenticated sender: board) "
    "by mai (Postfix) with ESMTPSA id ABC; Thu, 12 Nov 2026 09:00:00 +0000"
)


def test_sender_proved_and_board_headers():
    h = {
        "message-id": "<b@mai.internal>",
        "from": "board <board@mai.internal>",
        "received": RECEIVED_BOARD,
        "x-mai-item": "itm-k3q9x2m7p4ta",
        "x-mai-project": "proj",
    }
    assert m.sender_proved(h, "board")
    assert not m.sender_proved(h, "cursor")
    s = m.parse_summary(h)
    assert s["sender_proved"] is True
    assert s["item"] == "itm-k3q9x2m7p4ta" and s["project"] == "proj"


def test_forged_board_headers_ignored():
    received = RECEIVED_BOARD.replace("sender: board", "sender: cursor.lane-a")
    h = {
        "message-id": "<b@mai.internal>",
        "from": "board@mai.internal",
        "received": received,
        "x-mai-item": "itm-k3q9x2m7p4ta",
    }
    s = m.parse_summary(h)
    assert s["sender_proved"] is False and s["item"] is None
    h2 = {
        "message-id": "<c@mai.internal>",
        "from": "cursor.lane-a@mai.internal",
        "received": received,
        "x-mai-item": "itm-k3q9x2m7p4ta",
    }
    s2 = m.parse_summary(h2)
    assert s2["sender_proved"] is True and s2["item"] is None


def test_top_received_only():
    raw = (
        b"Received: from a by b; Thu, 12 Nov 2026 09:00:00 +0000\r\n"
        b"Received: from c (Authenticated sender: cursor) by d; Thu, 12 Nov 2026 09:00:00 +0000\r\n"
        b"From: cursor@mai.internal\r\nMessage-ID: <x@y>\r\n"
    )
    assert m.sender_proved(m.parse_headers_bytes(raw)) is False


def test_expand_and_parse_fetch():
    assert _expand("3:5,9") == ["3", "4", "5", "9"]
    data = [
        (
            b"1 (UID 7 FLAGS (\\Seen $MaiNotified) RFC822.SIZE 42 BODY[HEADER] {5}",
            b"hello",
        ),
        b")",
    ]
    rows = _parse_fetch(data)
    assert rows[0]["uid"] == 7 and rows[0]["flags"] == ["\\Seen", "$MaiNotified"]
    assert rows[0]["size"] == 42 and rows[0]["literal"] == b"hello"


# A small fake IMAP server for MOVE and IDLE


class FakeImap(socketserver.StreamRequestHandler):
    copyuid = True
    idle_delay = 0.3

    def w(self, line: str):
        self.wfile.write(line.encode() + b"\r\n")
        self.wfile.flush()

    def handle(self):
        self.w("* OK [CAPABILITY IMAP4rev1 IDLE MOVE UIDPLUS] fake ready")
        while True:
            line = self.rfile.readline()
            if not line:
                return
            parts = line.decode().strip().split(" ")
            tag, cmd = parts[0], parts[1].upper()
            rest = parts[2:]
            if cmd == "CAPABILITY":
                self.w("* CAPABILITY IMAP4rev1 IDLE MOVE UIDPLUS")
                self.w(f"{tag} OK done")
            elif cmd == "LOGIN":
                if rest[1].strip('"') == "good":
                    self.w(f"{tag} OK logged in")
                else:
                    self.w(f"{tag} NO [AUTHENTICATIONFAILED] failed")
            elif cmd in ("SELECT", "EXAMINE"):
                self.w("* 1 EXISTS")
                self.w("* OK [UIDVALIDITY 99] ok")
                self.w(f"{tag} OK [READ-WRITE] selected")
            elif cmd == "UID" and rest[0].upper() == "MOVE":
                if self.copyuid:
                    self.w(f"* OK [COPYUID 99 {rest[1]} 500] moved")
                    self.w("* 1 EXPUNGE")
                self.w(f"{tag} OK move done")
            elif cmd == "UID" and rest[0].upper() == "SEARCH":
                self.w("* SEARCH 4 8")
                self.w(f"{tag} OK search done")
            elif cmd == "IDLE":
                self.w("+ idling")
                if self.idle_delay is not None:
                    time.sleep(self.idle_delay)
                    self.w("* 2 EXISTS")
                done = self.rfile.readline()
                assert done.strip().upper() == b"DONE"
                self.w(f"{tag} OK idle done")
            elif cmd == "LOGOUT":
                self.w("* BYE bye")
                self.w(f"{tag} OK bye")
                return
            else:
                self.w(f"{tag} BAD unknown")


@pytest.fixture
def fake_server():
    servers = []

    def start(**attrs):
        handler = type("H", (FakeImap,), attrs)
        srv = socketserver.ThreadingTCPServer(("127.0.0.1", 0), handler)
        srv.daemon_threads = True
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        servers.append(srv)
        return srv.server_address[1]

    yield start
    for srv in servers:
        srv.shutdown()
        srv.server_close()


def _session(port, secret="good"):
    s = MailSession("cursor.lane-a", secret, imap_port=port, smtp_port=1)
    s._imap = s._open_imap()  # IMAP only; no SMTP in these tests
    return s


def test_login_failed(fake_server):
    port = fake_server()
    with pytest.raises(ToolError) as exc:
        MailSession("x", "bad", imap_port=port)._open_imap()
    assert exc.value.code == "LOGIN_FAILED"


def test_move_needs_copyuid(fake_server):
    s = _session(fake_server(copyuid=True))
    assert s.move("shared/cursor/INBOX", 4, "INBOX") is True
    s.close()
    s = _session(fake_server(copyuid=False))
    assert s.move("shared/cursor/INBOX", 4, "INBOX") is False
    s.close()


def test_search_and_find(fake_server):
    s = _session(fake_server())
    assert s.search("INBOX", "ALL") == [4, 8]
    assert s.find_message_id(["INBOX"], "<x@mai.internal>") == ("INBOX", 8)
    s.close()


def test_idle_new_mail(fake_server):
    s = _session(fake_server(idle_delay=0.2))
    t0 = time.monotonic()
    assert s.idle(["INBOX"], 5) is True
    assert time.monotonic() - t0 < 3
    s.close()


def test_idle_timeout(fake_server):
    s = _session(fake_server(idle_delay=None))
    t0 = time.monotonic()
    assert s.idle(["INBOX"], 1.5) is False
    assert 1.0 < time.monotonic() - t0 < 4
    # The connection still works after the timeout.
    assert s.search("INBOX", "ALL") == [4, 8]
    s.close()


def test_idle_two_mailboxes(fake_server):
    port = fake_server(idle_delay=0.3)
    s = _session(port)
    assert s.idle(["INBOX", "shared/cursor/INBOX"], 5) is True
    s.close()


@pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="needs Postfix and Dovecot")
def test_real_server_login():
    login = os.environ["MAI_IT_LOGIN"]
    secret = os.environ["MAI_IT_SECRET"]
    with MailSession(login, secret) as s:
        assert isinstance(s.search("INBOX", "ALL"), list)


def test_connect_imap_opens_no_smtp_connection(monkeypatch):
    from mai.platform.mailserver import MailSession

    session = MailSession("board", "secret")
    opened = []
    monkeypatch.setattr(
        session, "_open_imap", lambda: opened.append("imap") or object()
    )
    monkeypatch.setattr(
        session, "_open_smtp", lambda: opened.append("smtp") or object()
    )
    assert session.connect_imap() is session
    assert opened == ["imap"]


class _ReadOnlyImap:
    """A fake IMAP connection for a login with only lookup and read on the mailbox."""

    def __init__(self):
        self.selects = []

    def select(self, mailbox, readonly=False):
        import imaplib

        self.selects.append((mailbox, readonly))
        if not readonly:
            raise imaplib.IMAP4.readonly(f"{mailbox} is not writable")
        return "OK", [b"3"]


def test_a_read_only_mailbox_opens_read_only_and_is_not_a_lost_connection():
    from mai.platform.mailserver import MailSession

    session = MailSession("board", "secret")
    imap = _ReadOnlyImap()
    # The fake has only the IMAP4 calls that _select makes.
    conn = cast(imaplib.IMAP4, imap)
    assert session._select(conn, "shared/cursor/INBOX") is True
    assert session._select(conn, "shared/cursor/INBOX") is True
    session._selected = None
    assert session._select(conn, "shared/cursor/INBOX") is True
    assert imap.selects == [
        ('"shared/cursor/INBOX"', False),
        ('"shared/cursor/INBOX"', True),
        ('"shared/cursor/INBOX"', True),
    ]


class _SyncImap:
    """A fake IMAP connection that gives an expunge only after NOOP."""

    def __init__(self):
        self.synced = False
        self.calls = []

    def select(self, mailbox, readonly=False):
        return "OK", [b"1"]

    def noop(self):
        self.calls.append("NOOP")
        self.synced = True
        return "OK", [b""]

    def uid(self, command, *args):
        self.calls.append(command)
        return "OK", [b"" if self.synced else b"7"]


def test_contains_uid_syncs_before_the_search():
    from mai.platform.mailserver import MailSession

    session = MailSession("board", "secret")
    imap = _SyncImap()
    # The fake has only the IMAP4 calls that contains_uid makes.
    session._imap = cast(imaplib.IMAP4, imap)
    assert session.contains_uid("shared/cursor/INBOX", 7) is False
    assert imap.calls == ["NOOP", "SEARCH"]


# A small fake SMTP server like the internal smtpd of Postfix

TIMEOUT_REPLY = "421 4.4.2 fake Error: timeout exceeded"
SIZE_REPLY = "552 5.3.4 Error: message file too big"


class FakeSmtp(socketserver.StreamRequestHandler):
    """Accepts mail like Postfix, but the first `drops` connections end with 421.

    Postfix closes a connection that is idle for smtpd_timeout (300 s): it
    logs "timeout after AUTH", sends 421, and closes. The stage "auth" does
    the same at once after AUTH. The stages "rcpt" and "data" send the 421
    in place of the reply to RCPT or to the end of the data. The stage "size"
    refuses the data with 552, as Postfix does for a message that is too
    large, and keeps the connection.
    """

    stage = "auth"
    drops = 1
    connections: list
    accepted: list
    dropped: threading.Event

    def w(self, line: str):
        self.wfile.write(line.encode() + b"\r\n")
        self.wfile.flush()

    def drop(self):
        self.w(TIMEOUT_REPLY)
        self.dropped.set()

    def handle(self):
        self.connections.append(len(self.connections))
        dropping = len(self.connections) <= self.drops
        self.w("220 fake ESMTP")
        while True:
            line = self.rfile.readline()
            if not line:
                return
            verb = line.split(b" ", 1)[0].strip().upper()
            if verb == b"EHLO":
                self.w("250-fake")
                self.w("250 AUTH PLAIN")
            elif verb == b"AUTH":
                self.w("235 2.7.0 Authentication successful")
                if dropping and self.stage == "auth":
                    return self.drop()
            elif verb == b"MAIL":
                self.w("250 2.1.0 Ok")
            elif verb == b"RCPT":
                if dropping and self.stage == "rcpt":
                    return self.drop()
                self.w("250 2.1.5 Ok")
            elif verb == b"DATA":
                self.w("354 End data with <CR><LF>.<CR><LF>")
                data = []
                while (part := self.rfile.readline()) not in (b".\r\n", b""):
                    data.append(part)
                if dropping and self.stage == "data":
                    return self.drop()
                if dropping and self.stage == "size":
                    self.w(SIZE_REPLY)
                    continue
                self.accepted.append(b"".join(data))
                self.w("250 2.0.0 Ok: queued as FAKE")
            elif verb == b"QUIT":
                self.w("221 2.0.0 Bye")
                return
            else:
                self.w("502 5.5.2 Error: command not recognized")


@pytest.fixture
def fake_smtp():
    servers = []

    def start(**attrs):
        attrs.update(connections=[], accepted=[], dropped=threading.Event())
        handler = type("H", (FakeSmtp,), attrs)
        srv = socketserver.ThreadingTCPServer(("127.0.0.1", 0), handler)
        srv.daemon_threads = True
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        servers.append(srv)
        return srv.server_address[1], handler

    yield start
    for srv in servers:
        srv.shutdown()
        srv.server_close()


RAW = b"Subject: x\r\n\r\nbody\r\n"


@pytest.mark.parametrize("stage", ["auth", "rcpt", "data"])
def test_a_421_from_postfix_gives_a_new_connection_and_one_mail(fake_smtp, stage):
    port, server = fake_smtp(stage=stage)
    session = MailSession("cursor.lane-a", "good", imap_port=1, smtp_port=port)
    # The session logs in to SMTP when it connects, as a login does.
    session._smtp = session._open_smtp()
    if stage == "auth":
        # The connection is idle until Postfix ends it.
        assert server.dropped.wait(5)
    refused = session.send(RAW, "cursor.lane-a@mai.internal", ["codex@mai.internal"])
    assert refused == {}
    assert len(server.connections) == 2
    assert len(server.accepted) == 1
    session.close()


def test_a_second_421_gives_mail_server_down(fake_smtp):
    port, server = fake_smtp(stage="rcpt", drops=99)
    session = MailSession("cursor.lane-a", "good", imap_port=1, smtp_port=port)
    with pytest.raises(ToolError) as exc:
        session.send(RAW, "cursor.lane-a@mai.internal", ["codex@mai.internal"])
    assert exc.value.code == "MAIL_SERVER_DOWN"
    assert len(server.connections) == 2
    assert server.accepted == []


def test_a_552_reply_to_the_data_gives_body_too_large_and_no_second_send(fake_smtp):
    port, server = fake_smtp(stage="size")
    session = MailSession("cursor.lane-a", "good", imap_port=1, smtp_port=port)
    with pytest.raises(ToolError) as exc:
        session.send(RAW, "cursor.lane-a@mai.internal", ["codex@mai.internal"])
    assert (exc.value.code, exc.value.path) == ("BODY_TOO_LARGE", "body")
    assert len(server.connections) == 1
    assert server.accepted == []
    session.close()
