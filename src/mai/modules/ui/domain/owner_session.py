"""The signed-in owner sessions of the UI, and the rules of the cookie and the CSRF token."""

from __future__ import annotations

import hashlib
import hmac
import secrets
from abc import ABC, abstractmethod
from dataclasses import dataclass, replace
from datetime import datetime, timedelta

COOKIE = "mai_ui"
UI_TTL = timedelta(hours=12)
IDLE_LIMIT = timedelta(minutes=30)


@dataclass(frozen=True, slots=True)
class OwnerSession:
    key: str  # sha256 of the cookie token; the store never keeps the token
    csrf: str
    expires_at: datetime
    last_seen_at: datetime

    def is_live(self, now: datetime) -> bool:
        """A session ends 12 h after the sign-in, or 30 min after the last request."""
        return now < self.expires_at and now - self.last_seen_at <= IDLE_LIMIT

    def seen(self, now: datetime) -> OwnerSession:
        return replace(self, last_seen_at=now)


def token_key(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def new_token() -> str:
    return secrets.token_urlsafe(32)


def new_csrf() -> str:
    return secrets.token_urlsafe(24)


def csrf_matches(given: str | None, expected: str) -> bool:
    return hmac.compare_digest((given or "").encode(), expected.encode())


def session_cookie(token: str, max_age: int) -> str:
    """The Set-Cookie value: HttpOnly, SameSite=Strict, Path=/ui.

    The server listens on 127.0.0.1 with plain HTTP, thus the cookie has no
    Secure flag. Some browsers refuse a Secure cookie on plain HTTP.
    """
    return f"{COOKIE}={token}; Path=/ui; HttpOnly; SameSite=Strict; Max-Age={max_age}"


class OwnerSessionStore(ABC):
    @abstractmethod
    async def save(self, session: OwnerSession) -> None: ...

    @abstractmethod
    async def get(self, key: str) -> OwnerSession | None: ...

    @abstractmethod
    async def delete(self, key: str) -> None: ...

    @abstractmethod
    async def purge_expired(self, now: datetime) -> None: ...

    @abstractmethod
    async def clear(self) -> None: ...
