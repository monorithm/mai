# The mail server code closes connections and parses foreign headers on a
# best-effort basis: a failure there must never stop the caller.
# ruff: noqa: BLE001, SIM103
"""Build and parse mai messages.

The app writes every message with the same header set. A reader takes a
summary from the headers only, and never trusts the X-Mai-Item and
X-Mai-Project headers unless Postfix proved that the board sent the mail.
"""

from __future__ import annotations

import email.header
import email.parser
import email.utils
import hashlib
import re
import uuid
from datetime import UTC, datetime
from email.message import EmailMessage
from email.policy import SMTP

from mai.platform.contract import ToolError

DOMAIN = "mai.internal"
SUBJECT_MAX = 200
BODY_MAX = 64 * 1024
KINDS = ("request", "reply", "status", "notice", "signal")
PRIORITIES = ("low", "normal", "high")
BOARD_LOGIN = "board"

_CONTROL = re.compile(r"[\x00-\x1f\x7f]")
_MSGID = re.compile(r"<[^<>\s]+>")
_ITEM = re.compile(r"^itm-[a-z0-9]{12}$")
_AUTH_SENDER = re.compile(r"\(Authenticated sender:\s*([^\s)]+)\)", re.IGNORECASE)


def _error(code, path=None, **extra):
    return ToolError(code, path=path, **extra)


def address_of(login: str) -> str:
    """Give the full address of a login or of an address."""
    login = login.strip()
    if "@" in login:
        return login.lower()
    return f"{login.lower()}@{DOMAIN}"


def login_of(address: str) -> str:
    """Give the local part of an address in mai.internal, or the full address otherwise."""
    address = address.strip().lower()
    if address.endswith("@" + DOMAIN):
        return address[: -len(DOMAIN) - 1]
    return address


def display_name(login: str) -> str:
    """Give the display name: "cursor (lane-a)" for a session, the login otherwise."""
    if "." in login:
        agent, label = login.split(".", 1)
        return f"{agent} ({label})"
    return login


def validate_subject(subject: str) -> str:
    if not isinstance(subject, str):
        raise _error("INVALID_ARGUMENT", path="subject")
    if _CONTROL.search(subject):
        raise _error("REFUSED_HEADER", path="subject")
    if len(subject) > SUBJECT_MAX:
        raise _error("INVALID_ARGUMENT", path="subject", max_length=SUBJECT_MAX)
    return subject


def validate_body(body: str) -> str:
    if not isinstance(body, str):
        raise _error("INVALID_ARGUMENT", path="body")
    if len(body.encode("utf-8")) > BODY_MAX:
        raise _error("BODY_TOO_LARGE", path="body", max_bytes=BODY_MAX)
    return body


def new_message_id() -> str:
    return f"<msg-{uuid.uuid4()}@{DOMAIN}>"


def ref_of(message_id: str) -> str:
    return "ref-" + hashlib.sha256(message_id.strip().encode()).hexdigest()[:16]


def thread_ref_of(root_message_id: str) -> str:
    return "thr-" + hashlib.sha256(root_message_id.strip().encode()).hexdigest()[:16]


def message_ids(value: str | None) -> list[str]:
    """Give the message ids in a References or In-Reply-To value, in order."""
    if not value:
        return []
    return _MSGID.findall(value)


def _addr_list(values) -> list[str]:
    if values is None:
        return []
    if isinstance(values, str):
        values = [values]
    return [address_of(v) for v in values if v and v.strip()]


def build_message(
    from_login: str,
    display: str | None,
    to,
    cc=None,
    subject: str = "",
    body: str = "",
    kind: str = "request",
    priority: str = "normal",
    expects_reply: bool = False,
    item: str | None = None,
    project: str | None = None,
    in_reply_to: str | None = None,
    references: str | list[str] | None = None,
    message_id: str | None = None,
    date: datetime | None = None,
) -> bytes:
    """Give the raw bytes (CRLF line ends) of one plain-text message."""
    validate_subject(subject)
    validate_body(body)
    if kind not in KINDS:
        raise _error("INVALID_ARGUMENT", path="kind")
    if priority not in PRIORITIES:
        raise _error("INVALID_ARGUMENT", path="priority")
    to_list = _addr_list(to)
    if not to_list:
        raise _error("INVALID_ARGUMENT", path="to")
    cc_list = _addr_list(cc)

    msg = EmailMessage(policy=SMTP)
    from_addr = address_of(from_login)
    msg["From"] = email.utils.formataddr(
        (
            display if display is not None else display_name(login_of(from_addr)),
            from_addr,
        )
    )
    msg["To"] = ", ".join(to_list)
    if cc_list:
        msg["Cc"] = ", ".join(cc_list)
    msg["Subject"] = subject
    when = date or datetime.now(UTC)
    msg["Date"] = email.utils.format_datetime(when.astimezone(UTC))
    msg["Message-ID"] = message_id or new_message_id()
    if in_reply_to:
        msg["In-Reply-To"] = in_reply_to
    if references:
        if isinstance(references, (list, tuple)):
            references = " ".join(references)
        msg["References"] = references
    msg["X-Mai-Kind"] = kind
    msg["X-Mai-Priority"] = priority
    msg["X-Mai-Expects-Reply"] = "yes" if expects_reply else "no"
    if item:
        msg["X-Mai-Item"] = item
    if project:
        msg["X-Mai-Project"] = project
    msg.set_content(body, charset="utf-8")
    return msg.as_bytes()


def _first(headers: dict, name: str) -> str | None:
    value = headers.get(name)
    if isinstance(value, (list, tuple)):
        value = value[0] if value else None
    if value is None:
        return None
    return str(value).strip()


def _top_received(headers: dict) -> str | None:
    value = headers.get("received")
    if isinstance(value, (list, tuple)):
        return str(value[0]) if value else None
    return None if value is None else str(value)


def _decode(value: str | None) -> str:
    if not value:
        return ""
    try:
        return str(email.header.make_header(email.header.decode_header(value)))
    except Exception:
        return value


def _addresses(value: str | None) -> list[str]:
    if not value:
        return []
    return [a.lower() for _, a in email.utils.getaddresses([value]) if a]


def _iso_date(value: str | None) -> str | None:
    if not value:
        return None
    try:
        dt = email.utils.parsedate_to_datetime(value)
    except Exception:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=UTC)
    return dt.astimezone(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def authenticated_sender(headers: dict) -> str | None:
    """Give the SASL login that Postfix wrote in the top Received header."""
    received = _top_received(headers)
    if not received:
        return None
    match = _AUTH_SENDER.search(received)
    return match.group(1).lower() if match else None


def sender_proved(headers: dict, from_login: str | None = None) -> bool:
    """True only if the top Received header names the same login as From.

    With from_login, the login must also be equal to that login.
    """
    auth = authenticated_sender(headers)
    if not auth:
        return False
    auth_login = login_of(auth)
    froms = _addresses(_first(headers, "from"))
    if len(froms) != 1 or login_of(froms[0]) != auth_login:
        return False
    if from_login is not None and login_of(from_login) != auth_login:
        return False
    return True


def parse_summary(headers: dict) -> dict:
    """Give the summary fields of a message from its headers.

    The headers map has lower-case names. A value can be a string or a list
    of strings (the first is the top header).
    """
    message_id = _first(headers, "message-id") or ""
    refs = message_ids(_first(headers, "references"))
    root = (
        refs[0]
        if refs
        else (message_ids(_first(headers, "in-reply-to")) or [message_id])[0]
    )
    froms = _addresses(_first(headers, "from"))
    from_addr = froms[0] if froms else ""
    kind = (_first(headers, "x-mai-kind") or "").lower()
    priority = (_first(headers, "x-mai-priority") or "").lower()
    proved = sender_proved(headers)
    item = None
    project = None
    if proved and login_of(from_addr) == BOARD_LOGIN:
        raw_item = _first(headers, "x-mai-item")
        item = raw_item if raw_item and _ITEM.match(raw_item) else None
        project = _first(headers, "x-mai-project") or None
    expects = (_first(headers, "x-mai-expects-reply") or "").lower() in (
        "yes",
        "true",
        "1",
    )
    return {
        "ref": ref_of(message_id) if message_id else None,
        "thread_ref": thread_ref_of(root) if root else None,
        "message_id": message_id or None,
        "root_message_id": root or None,
        "from": from_addr,
        "sender_proved": proved,
        "to": _addresses(_first(headers, "to")),
        "cc": _addresses(_first(headers, "cc")),
        "subject": _decode(_first(headers, "subject")),
        "kind": kind if kind in KINDS else "unknown",
        "priority": priority if priority in PRIORITIES else "unknown",
        "date": _iso_date(_first(headers, "date")),
        "item": item,
        "project": project,
        "in_reply_to": (message_ids(_first(headers, "in-reply-to")) or [None])[0],
        "references": refs,
        "expects_reply": expects,
    }


def parse_headers_bytes(raw: bytes) -> dict:
    """Parse a raw header block into a lower-case name -> str map.

    For a header that occurs more than one time, the map keeps the first
    (top) one. That is the Received header that the last server added.
    """
    parsed = email.parser.BytesHeaderParser().parsebytes(raw)
    out: dict = {}
    for name, value in parsed.items():
        key = name.lower()
        if key not in out:
            out[key] = " ".join(str(value).split())
    return out


def parse_body(raw: bytes) -> str:
    """Give the text/plain body of a raw message."""
    from email.policy import default

    msg = email.parser.BytesParser(policy=default).parsebytes(raw)
    part = msg.get_body(preferencelist=("plain",)) if msg.is_multipart() else msg
    if part is None:
        return ""
    try:
        return part.get_content()
    except Exception:
        # With decode=True the payload of a single part is bytes, else None.
        payload = part.get_payload(decode=True)
        data = payload if isinstance(payload, bytes) else b""
        return data.decode("utf-8", "replace")
