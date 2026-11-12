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


class SentRequests(ABC):
    """The retry keys of send_message and reply: one row for each login and request_id."""

    @abstractmethod
    async def reserve(
        self, login: str, request_id: str, content_sha: str, message_id: str
    ) -> tuple[str, bool]:
        """Keep the key. Return (message_id, duplicate). A key with a different content raises MailError RETRY_KEY_REUSED."""

    @abstractmethod
    async def release(self, login: str, request_id: str, message_id: str) -> None:
        """Remove a key whose send failed, so that a retry can send."""


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
