"""The ports of the mail module."""

from __future__ import annotations

import datetime as dt
from abc import ABC, abstractmethod
from dataclasses import dataclass


@dataclass(frozen=True, slots=True)
class SessionEntry:
    login: str
    label: str | None
    last_login_at: dt.datetime | None


@dataclass(frozen=True, slots=True)
class Reservation:
    """The result of a reserve: the mail of the key, and if that mail is sent already."""

    message_id: str
    duplicate: bool
    sent_at: dt.datetime | None = None


class SentRequests(ABC):
    """The retry keys of send_message and reply: one row for each login and request_id.

    A row is pending while its send runs, sent after Postfix took the mail,
    and failed after the send failed. The app role has no DELETE right, thus
    a row never goes away.
    """

    @abstractmethod
    async def reserve(
        self, login: str, request_id: str, content_sha: str, message_id: str
    ) -> Reservation:
        """Keep the key for the mail message_id.

        A key of a sent mail gives a duplicate with the message_id and the
        date of that mail. A key whose mail did not go out gets the new
        message_id, thus a retry sends. A key with a different content raises
        MailError RETRY_KEY_REUSED.
        """

    @abstractmethod
    async def mark_sent(
        self, login: str, request_id: str, message_id: str, sent_at: dt.datetime
    ) -> None:
        """Keep the date of the mail of the key after Postfix took it."""

    @abstractmethod
    async def release(self, login: str, request_id: str, message_id: str) -> None:
        """Mark the key of a failed send, so that a retry sends."""


class Directory(ABC):
    """The principals that can get mail."""

    @abstractmethod
    async def known_logins(self, logins: list[str]) -> set[str]:
        """Return the logins that are agents or live sessions."""

    @abstractmethod
    async def agents(self) -> dict[str, list[SessionEntry]]:
        """Return each agent with its live sessions, ordered by login."""


class MailboxAccess(ABC):
    """Calls to the mail session of a login. A lost connection gets one reconnect, then MailError MAIL_SERVER_DOWN."""

    @abstractmethod
    async def call(self, mail: object, method: str, *args, **kwargs): ...
