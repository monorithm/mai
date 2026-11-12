"""The read-only mail views of the UI: a list of mails and one mail as text."""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from email import message_from_bytes, policy
from email.message import EmailMessage

COPIES = "copies"
OWNER = "owner"
SHOWN_HEADERS = ("From", "To", "Cc", "Date", "Subject", "Message-ID", "In-Reply-To")


@dataclass(frozen=True, slots=True)
class MailSummary:
    uid: str
    received: str
    sender: str
    subject: str
    message_id: str = ""


@dataclass(frozen=True, slots=True)
class MailText:
    uid: str
    headers: list[tuple[str, str]]
    body: str
    message_id: str = ""
    envelope: dict = field(default_factory=dict)


class MailboxMissingError(Exception):
    """The user or the mailbox does not exist, or the mail server cannot read it."""


class MailReader(ABC):
    @abstractmethod
    async def messages(self, user: str, search: str = "") -> list[MailSummary]:
        """The mails of the INBOX of user, newest first."""

    @abstractmethod
    async def raw(self, user: str, uid: str) -> bytes | None:
        """The full source of one mail of the INBOX of user."""


class EnvelopeLookup(ABC):
    @abstractmethod
    async def find(self, message_ids: list[str]) -> dict[str, dict]:
        """The envelope records by Message-ID. An empty dict when there is none."""


def text_of(uid: str, raw: bytes) -> MailText:
    """Parse a mail. Keep only the headers of SHOWN_HEADERS and the text/plain body.

    The UI never renders the HTML of a mail. A mail with only HTML gives a note.
    """
    msg = message_from_bytes(raw, policy=policy.default)
    assert isinstance(msg, EmailMessage)
    headers = [(h, str(msg[h])) for h in SHOWN_HEADERS if msg[h] is not None]
    part = msg.get_body(preferencelist=("plain",))
    if part is None:
        body = "(This mail has no text part. The UI does not show HTML.)"
    else:
        assert isinstance(part, EmailMessage)
        try:
            body = str(part.get_content())
        except (LookupError, ValueError):
            payload = part.get_payload(decode=True)
            body = (
                payload.decode("utf-8", "replace") if isinstance(payload, bytes) else ""
            )
    return MailText(uid, headers, body, str(msg["Message-ID"] or "").strip())


@dataclass(frozen=True, slots=True)
class ThreadClaim:
    agent: str
    root_msg_id: str
    holder: str
    lease_end: str
    live: bool


class ThreadLookup(ABC):
    @abstractmethod
    async def claims(self) -> list[ThreadClaim]:
        """The claims of the threads, newest lease first. Empty when there is none."""
