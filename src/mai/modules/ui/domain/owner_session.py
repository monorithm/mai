"""The signed-in owner sessions of the UI, and the rules of the cookie and the CSRF token."""

from __future__ import annotations

import hashlib
import hmac
import secrets
from abc import ABC, abstractmethod
from dataclasses import dataclass
from datetime import datetime, timedelta

COOKIE = "smc_ui"
UI_TTL = timedelta(hours=8)


@dataclass(frozen=True, slots=True)
class OwnerSession:
    key: str  # sha256 of the cookie token; the token itself is never kept
    csrf: str
    expires_at: datetime

    def is_live(self, now: datetime) -> bool:
        return self.expires_at >= now


def token_key(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def new_token() -> str:
    return secrets.token_urlsafe(32)


def new_csrf() -> str:
    return secrets.token_urlsafe(24)


def csrf_matches(given: str | None, expected: str) -> bool:
    return hmac.compare_digest((given or "").encode(), expected.encode())


def session_cookie(token: str, max_age: int) -> str:
    """The Set-Cookie value: HttpOnly, SameSite=Strict, Secure."""
    return f"{COOKIE}={token}; Path=/; HttpOnly; SameSite=Strict; Secure; Max-Age={max_age}"


class OwnerSessionStore(ABC):
    @abstractmethod
    async def save(self, session: OwnerSession) -> None: ...

    @abstractmethod
    async def get(self, key: str) -> OwnerSession | None: ...

    @abstractmethod
    async def delete(self, key: str) -> None: ...

    @abstractmethod
    async def purge_expired(self, now: datetime) -> None: ...
