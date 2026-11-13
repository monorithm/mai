# The mail server code closes connections and parses foreign headers on a
# best-effort basis: a failure there must never stop the caller.
# ruff: noqa: BLE001, S110
"""IMAP and SMTP client of one mail login.

A MailSession holds one IMAP connection to Dovecot and one SMTP connection
to the internal submission port of Postfix. All IMAP calls use UIDs. The
class is safe for use from more than one thread: a lock serializes the
commands on each connection.
"""

from __future__ import annotations

import imaplib
import re
import select
import smtplib
import threading
import time
from typing import Any

from mai.platform.contract import ToolError

DEFAULT_TIMEOUT = 30

_FETCH_UID = re.compile(rb"UID (\d+)")
_FETCH_FLAGS = re.compile(rb"FLAGS \(([^)]*)\)")
_FETCH_SIZE = re.compile(rb"RFC822\.SIZE (\d+)")
_COPYUID = re.compile(r"COPYUID\s+(\d+)\s+([\d:,]+)\s+([\d:,]+)", re.IGNORECASE)
_UNTAGGED_NEW = re.compile(rb"^\*\s+\d+\s+(EXISTS|RECENT)", re.IGNORECASE)


def _error(code, path=None, **extra):
    return ToolError(code, path=path, **extra)


def _quote(mailbox: str) -> str:
    """Quote a mailbox name for an IMAP command."""
    return '"' + mailbox.replace("\\", "\\\\").replace('"', '\\"') + '"'


def _uid_set(uids) -> str:
    return ",".join(str(int(u)) for u in uids)


def _flag_list(flags) -> str:
    return "(" + " ".join(flags) + ")"


def _text(data) -> str:
    out = []
    for part in data or []:
        if isinstance(part, tuple):
            part = part[0]
        if isinstance(part, bytes):
            part = part.decode("utf-8", "replace")
        if part is not None:
            out.append(str(part))
    return " ".join(out)


class MailSession:
    """One login on Dovecot (IMAP) and Postfix (SMTP submission)."""

    def __init__(
        self,
        login: str,
        secret: str,
        imap_host: str = "127.0.0.1",
        imap_port: int = 1143,
        smtp_host: str = "127.0.0.1",
        smtp_port: int = 1588,
        timeout: float = DEFAULT_TIMEOUT,
    ):
        self.login = login
        self._secret = secret
        self.imap_host = imap_host
        self.imap_port = imap_port
        self.smtp_host = smtp_host
        self.smtp_port = smtp_port
        self.timeout = timeout
        self._imap: imaplib.IMAP4 | None = None
        self._smtp: smtplib.SMTP | None = None
        self._selected: tuple[str, bool] | None = None
        # The mailboxes where this login has no write right: Dovecot gives READ-ONLY.
        self._read_only: set[str] = set()
        self._imap_lock = threading.RLock()
        self._smtp_lock = threading.RLock()

    def __repr__(self) -> str:  # never show the secret
        return f"MailSession(login={self.login!r})"

    # Connections

    def _open_imap(self) -> imaplib.IMAP4:
        try:
            imap = imaplib.IMAP4(self.imap_host, self.imap_port, timeout=self.timeout)
        except (OSError, imaplib.IMAP4.error) as exc:
            raise _error("MAIL_SERVER_DOWN") from exc
        try:
            imap.login(self.login, self._secret)
        except imaplib.IMAP4.error as exc:
            try:
                imap.shutdown()
            except Exception:
                pass
            raise _error("LOGIN_FAILED") from exc
        except OSError as exc:
            raise _error("MAIL_SERVER_DOWN") from exc
        return imap

    def _open_smtp(self) -> smtplib.SMTP:
        try:
            smtp = smtplib.SMTP(self.smtp_host, self.smtp_port, timeout=self.timeout)
            smtp.ehlo("mai-app")
        except (OSError, smtplib.SMTPException) as exc:
            raise _error("MAIL_SERVER_DOWN") from exc
        try:
            smtp.login(self.login, self._secret)
        except smtplib.SMTPAuthenticationError as exc:
            try:
                smtp.quit()
            except Exception:
                pass
            raise _error("LOGIN_FAILED") from exc
        except (OSError, smtplib.SMTPException) as exc:
            raise _error("MAIL_SERVER_DOWN") from exc
        return smtp

    def connect(self) -> MailSession:
        """Log in on IMAP and on SMTP. Raises LOGIN_FAILED or MAIL_SERVER_DOWN."""
        with self._imap_lock:
            if self._imap is None:
                self._imap = self._open_imap()
                self._selected = None
        with self._smtp_lock:
            if self._smtp is None:
                self._smtp = self._open_smtp()
        return self

    def connect_imap(self) -> MailSession:
        """Log in on IMAP only, for a reader that never sends. Raises LOGIN_FAILED or MAIL_SERVER_DOWN."""
        with self._imap_lock:
            if self._imap is None:
                self._imap = self._open_imap()
                self._selected = None
        return self

    def close(self) -> None:
        """Log out of IMAP and quit SMTP. Never raises."""
        with self._imap_lock:
            if self._imap is not None:
                try:
                    self._imap.logout()
                except Exception:
                    pass
                self._imap = None
                self._selected = None
        with self._smtp_lock:
            if self._smtp is not None:
                try:
                    self._smtp.quit()
                except Exception:
                    pass
                self._smtp = None

    def __enter__(self):
        return self.connect()

    def __exit__(self, *exc):
        self.close()

    def _imap_conn(self) -> imaplib.IMAP4:
        if self._imap is None:
            self._imap = self._open_imap()
            self._selected = None
        return self._imap

    def _run(self, fn):
        """Run fn(imap) with one reconnect if the connection dropped."""
        with self._imap_lock:
            for attempt in (0, 1):
                imap = self._imap_conn()
                try:
                    return fn(imap)
                except (imaplib.IMAP4.abort, OSError) as exc:
                    try:
                        imap.shutdown()
                    except Exception:
                        pass
                    self._imap = None
                    self._selected = None
                    if attempt:
                        raise _error("MAIL_SERVER_DOWN") from exc
        raise _error("MAIL_SERVER_DOWN")  # not reached

    def _select(
        self, imap: imaplib.IMAP4, mailbox: str, readonly: bool = False
    ) -> bool:
        """Select a mailbox. Returns False if it does not exist or has no access.

        A login with only the rights lookup and read gets READ-ONLY from
        Dovecot for a read-write select, and imaplib raises IMAP4.readonly.
        That is not a lost connection: the mailbox is selected, and a read
        works. Thus the mailbox opens read-only, and later selects of it are
        read-only at once. A write to it then gets NO from Dovecot.
        """
        if mailbox in self._read_only:
            readonly = True
        key = (mailbox, readonly)
        if self._selected == key:
            # Dovecot gives a change of a different process, for example a
            # new mail in the queue, to this view only at the next sync.
            typ, _ = imap.noop()
            if typ != "OK":
                self._selected = None
                return False
            return True
        try:
            typ, _ = imap.select(_quote(mailbox), readonly=readonly)
        except imaplib.IMAP4.readonly:
            self._read_only.add(mailbox)
            key = (mailbox, True)
            typ, _ = imap.select(_quote(mailbox), readonly=True)
        if typ != "OK":
            self._selected = None
            return False
        self._selected = key
        return True

    # SMTP

    def send(self, raw_bytes: bytes, mail_from: str, rcpt_to: list) -> dict:
        """Submit one message. Returns the refused recipients (empty on success).

        Raises RECIPIENT_UNKNOWN if Postfix refuses all recipients or the
        sender, BODY_TOO_LARGE if Postfix refuses the data with 552, and
        MAIL_SERVER_DOWN if Postfix does not reply.

        The SMTP connection stays open after the login. Postfix closes a
        connection with no command for smtpd_timeout (300 s): it logs
        "timeout after AUTH" and sends 421. Postfix did not take the mail,
        thus a lost connection or a 421 gets one new connection and one more
        send.
        """
        with self._smtp_lock:
            for attempt in (0, 1):
                if self._smtp is None:
                    self._smtp = self._open_smtp()
                try:
                    refused = self._smtp.sendmail(mail_from, list(rcpt_to), raw_bytes)
                    return {
                        k: (
                            v[0],
                            v[1].decode("utf-8", "replace")
                            if isinstance(v[1], bytes)
                            else v[1],
                        )
                        for k, v in (refused or {}).items()
                    }
                except smtplib.SMTPRecipientsRefused as exc:
                    if not _closing(exc):
                        raise _error(
                            "RECIPIENT_UNKNOWN",
                            path="to",
                            refused=sorted(exc.recipients),
                        ) from exc
                    self._drop_smtp()
                    if attempt:
                        raise _error("MAIL_SERVER_DOWN") from exc
                except smtplib.SMTPSenderRefused as exc:
                    if not _closing(exc):
                        raise _error("RECIPIENT_UNKNOWN", path="from") from exc
                    self._drop_smtp()
                    if attempt:
                        raise _error("MAIL_SERVER_DOWN") from exc
                # SMTPDataError is a subclass of OSError, thus its branch is first.
                except smtplib.SMTPDataError as exc:
                    if exc.smtp_code == 552:
                        raise _error("BODY_TOO_LARGE", path="body") from exc
                    if not _closing(exc):
                        raise _error("MAIL_SERVER_DOWN") from exc
                    self._drop_smtp()
                    if attempt:
                        raise _error("MAIL_SERVER_DOWN") from exc
                except (smtplib.SMTPServerDisconnected, OSError) as exc:
                    self._drop_smtp()
                    if attempt:
                        raise _error("MAIL_SERVER_DOWN") from exc
        raise _error("MAIL_SERVER_DOWN")  # not reached

    # IMAP

    def append(self, mailbox: str, raw_bytes: bytes, flags=()) -> bool:
        def op(imap):
            typ, _ = imap.append(
                _quote(mailbox), _flag_list(flags) if flags else None, None, raw_bytes
            )
            return typ == "OK"

        return self._run(op)

    def search(self, mailbox: str, criteria: str) -> list[int]:
        def op(imap):
            if not self._select(imap, mailbox):
                return []
            typ, data = imap.uid("SEARCH", criteria)
            if typ != "OK":
                return []
            return [int(x) for x in _text(data).split() if x.isdigit()]

        return self._run(op)

    def contains_uid(self, mailbox: str, uid: int) -> bool:
        """True when the mailbox has the UID at this time.

        NOOP comes first: a UID SEARCH alone can still give a mail that a
        different process moved, because Dovecot gives the expunge to this
        session only at the next sync.
        """

        def op(imap):
            if not self._select(imap, mailbox):
                return False
            imap.noop()
            typ, data = imap.uid("SEARCH", f"UID {int(uid)}")
            if typ != "OK":
                return False
            return str(int(uid)) in _text(data).split()

        return self._run(op)

    def fetch_headers(self, mailbox: str, uids) -> list[dict]:
        """Give uid, flags, headers (lower-case name -> first value) and size of each UID."""
        from .message import parse_headers_bytes

        uids = list(uids)
        if not uids:
            return []

        def op(imap):
            if not self._select(imap, mailbox):
                return []
            typ, data = imap.uid(
                "FETCH", _uid_set(uids), "(UID FLAGS RFC822.SIZE BODY.PEEK[HEADER])"
            )
            if typ != "OK":
                return []
            return _parse_fetch(data, with_headers=True)

        rows = self._run(op)
        out: list[dict[str, Any]] = []
        for row in rows:
            out.append(
                {
                    "uid": row["uid"],
                    "flags": row["flags"],
                    "headers": parse_headers_bytes(row.get("literal") or b""),
                    "size": row["size"],
                }
            )
        out.sort(key=lambda r: r["uid"])
        return out

    def fetch_raw(self, mailbox: str, uid: int) -> bytes | None:
        """Give the full raw message without setting the Seen flag."""

        def op(imap):
            if not self._select(imap, mailbox):
                return None
            typ, data = imap.uid("FETCH", str(int(uid)), "(UID BODY.PEEK[])")
            if typ != "OK":
                return None
            rows = _parse_fetch(data, with_headers=True)
            for row in rows:
                if row["uid"] == int(uid):
                    return row.get("literal")
            return None

        return self._run(op)

    def store_flags(self, mailbox: str, uids, add=(), remove=()) -> bool:
        uids = list(uids)
        if not uids:
            return True

        def op(imap):
            if not self._select(imap, mailbox):
                return False
            ok = True
            if add:
                typ, _ = imap.uid(
                    "STORE", _uid_set(uids), "+FLAGS.SILENT", _flag_list(add)
                )
                ok = ok and typ == "OK"
            if remove:
                typ, _ = imap.uid(
                    "STORE", _uid_set(uids), "-FLAGS.SILENT", _flag_list(remove)
                )
                ok = ok and typ == "OK"
            return ok

        return self._run(op)

    def move(self, mailbox: str, uid: int, dest: str) -> bool:
        """UID MOVE one message. True only when the server gave COPYUID.

        A move with no COPYUID means a different client moved or expunged
        the message first, so the caller must not count it.
        """

        def op(imap):
            if not self._select(imap, mailbox):
                return False
            imap.response("COPYUID")  # drop an old response code
            try:
                typ, data = imap.uid("MOVE", str(int(uid)), _quote(dest))
            except imaplib.IMAP4.error:
                return False
            if typ != "OK":
                return False
            _, codes = imap.response("COPYUID")
            texts = [_text([c]) for c in (codes or []) if c is not None]
            texts.append(_text(data))
            for text in texts:
                m = _COPYUID.search(
                    text if "COPYUID" in text.upper() else "COPYUID " + text
                )
                if m and str(int(uid)) in _expand(m.group(2)):
                    return True
            return False

        return self._run(op)

    def copyuid_of(self, text: str):
        """Parse a COPYUID text into (uidvalidity, source uids, dest uids)."""
        m = _COPYUID.search(text)
        if not m:
            return None
        return int(m.group(1)), _expand(m.group(2)), _expand(m.group(3))

    def find_message_id(self, mailboxes, message_id: str):
        """Give (mailbox, uid) of the first mailbox that has this Message-ID."""
        quoted = '"' + message_id.replace("\\", "\\\\").replace('"', '\\"') + '"'
        for mailbox in mailboxes:
            uids = self.search(mailbox, f"HEADER Message-ID {quoted}")
            if uids:
                return mailbox, uids[-1]
        return None

    def status_uidnext(self, mailbox: str) -> int | None:
        def op(imap):
            typ, data = imap.status(_quote(mailbox), "(UIDNEXT)")
            if typ != "OK":
                return None
            m = re.search(r"UIDNEXT (\d+)", _text(data))
            return int(m.group(1)) if m else None

        return self._run(op)

    # IDLE

    def idle(self, mailboxes, timeout_sec: float) -> bool:
        """Wait for new mail in any of the mailboxes. True on new mail, False on timeout.

        The main connection idles on the first mailbox. Each other mailbox
        gets its own short-lived connection in a thread.
        """
        mailboxes = list(mailboxes)
        if not mailboxes or timeout_sec <= 0:
            return False
        deadline = time.monotonic() + timeout_sec
        stop = threading.Event()
        found = threading.Event()
        errors: list = []

        def watch_extra(mailbox):
            try:
                imap = self._open_imap()
            except Exception as exc:
                errors.append(exc)
                return
            try:
                typ, _ = imap.select(_quote(mailbox), readonly=True)
                if typ == "OK" and _raw_idle(imap, deadline, stop):
                    found.set()
                    stop.set()
            except Exception as exc:
                errors.append(exc)
            finally:
                try:
                    imap.logout()
                except Exception:
                    pass

        threads = [
            threading.Thread(target=watch_extra, args=(m,), daemon=True)
            for m in mailboxes[1:]
        ]
        for t in threads:
            t.start()

        def op(imap):
            if not self._select(imap, mailboxes[0], readonly=True):
                return False
            return _raw_idle(imap, deadline, stop)

        try:
            with self._imap_lock:
                imap = self._imap_conn()
                try:
                    if op(imap):
                        found.set()
                except (imaplib.IMAP4.abort, OSError) as exc:
                    self._drop_imap()
                    raise _error("MAIL_SERVER_DOWN") from exc
                finally:
                    # A readonly select must not stay for later writes.
                    self._selected = None
        finally:
            stop.set()
            for t in threads:
                t.join(timeout=5)
        return found.is_set()

    def _drop_smtp(self):
        if self._smtp is not None:
            try:
                self._smtp.close()
            except Exception:
                pass
        self._smtp = None

    def _drop_imap(self):
        if self._imap is not None:
            try:
                self._imap.shutdown()
            except Exception:
                pass
        self._imap = None
        self._selected = None


def _raw_idle(imap: imaplib.IMAP4, deadline: float, stop: threading.Event) -> bool:
    """Run one IDLE command until new mail, the deadline, or the stop event.

    The command reads the socket directly with select(), because a read
    timeout makes the buffered file of imaplib unusable. The same code works
    on each Python version, with or without IMAP4.idle(). Before IDLE the
    buffered file is empty, because each earlier command read its reply up
    to the tagged line.
    """
    tag = imap._new_tag()
    sock = imap.sock
    buf = bytearray()

    def read_line(wait: float):
        """Give one line, or None if no full line came in the wait time."""
        end = time.monotonic() + wait
        while b"\n" not in buf:
            remaining = end - time.monotonic()
            if remaining <= 0:
                return None
            ready, _, _ = select.select([sock], [], [], min(remaining, 1.0))
            if stop.is_set() and not ready:
                return None
            if ready:
                chunk = sock.recv(4096)
                if not chunk:
                    raise imaplib.IMAP4.abort("connection closed during IDLE")
                buf.extend(chunk)
        idx = buf.index(b"\n") + 1
        line = bytes(buf[:idx])
        del buf[:idx]
        return line

    imap.send(tag + b" IDLE\r\n")
    line = read_line(max(5.0, imap_timeout(imap)))
    if line is None:
        raise imaplib.IMAP4.abort("no reply to IDLE")
    if not line.startswith(b"+"):
        return False
    new_mail = False
    while not stop.is_set():
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        line = read_line(min(1.0, remaining))
        if line is not None and _UNTAGGED_NEW.match(line):
            new_mail = True
            break
    imap.send(b"DONE\r\n")
    while True:
        line = read_line(max(5.0, imap_timeout(imap)))
        if line is None:
            raise imaplib.IMAP4.abort("no reply to DONE")
        if _UNTAGGED_NEW.match(line):
            new_mail = True
        if line.startswith(tag + b" "):
            break
    return new_mail


def _closing(
    exc: smtplib.SMTPResponseException | smtplib.SMTPRecipientsRefused,
) -> bool:
    """True when the reply is 421: the server closes the connection and did not take the mail."""
    if isinstance(exc, smtplib.SMTPRecipientsRefused):
        return any(reply[0] == 421 for reply in exc.recipients.values())
    return exc.smtp_code == 421


def imap_timeout(imap: imaplib.IMAP4) -> float:
    try:
        return float(imap.sock.gettimeout() or DEFAULT_TIMEOUT)
    except Exception:
        return float(DEFAULT_TIMEOUT)


def _expand(uid_set: str) -> list[str]:
    out: list[str] = []
    for part in uid_set.split(","):
        if ":" in part:
            a, b = part.split(":", 1)
            lo, hi = sorted((int(a), int(b)))
            out.extend(str(i) for i in range(lo, hi + 1))
        elif part:
            out.append(str(int(part)))
    return out


def _parse_fetch(data, with_headers: bool = False) -> list[dict]:
    """Parse the data of a UID FETCH reply into rows of uid, flags, size, literal."""
    rows: list[dict] = []
    current: dict | None = None
    for part in data or []:
        if isinstance(part, tuple):
            prefix, literal = part[0], part[1]
            current = {"meta": prefix, "literal": literal}
            rows.append(current)
        elif isinstance(part, bytes):
            if current is not None and not re.match(rb"^\d+ \(", part):
                current["meta"] += b" " + part
                current = None
            else:
                current = {"meta": part, "literal": None}
                rows.append(current)
                current = None
    out = []
    for row in rows:
        meta = row["meta"]
        m = _FETCH_UID.search(meta)
        if not m:
            continue
        flags_m = _FETCH_FLAGS.search(meta)
        size_m = _FETCH_SIZE.search(meta)
        out.append(
            {
                "uid": int(m.group(1)),
                "flags": flags_m.group(1).decode().split() if flags_m else [],
                "size": int(size_m.group(1))
                if size_m
                else (len(row["literal"]) if row["literal"] else 0),
                "literal": row["literal"],
            }
        )
    return out
