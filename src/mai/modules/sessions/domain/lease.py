"""A lease gives one app one session of an agent until the app stops.

The lease token is "lease_" plus 43 base64url characters (32 random bytes).
The server keeps only the SHA-256 hash of the token. A lease ends 90 s after
the last heartbeat.
"""

from __future__ import annotations

import datetime as dt
import hashlib
import re
import secrets
from dataclasses import dataclass, replace

TOKEN_PREFIX = "lease_"
HEARTBEAT_SECONDS = 30
EXPIRES_SECONDS = 90
MAX_SESSIONS = 8
_TOKEN_RE = re.compile(r"^lease_[A-Za-z0-9_-]{43}$")


def new_token() -> str:
    """A new lease token with 256 random bits."""
    return TOKEN_PREFIX + secrets.token_urlsafe(32)


def is_token(text: object) -> bool:
    """True when TEXT has the form of a lease token."""
    return isinstance(text, str) and bool(_TOKEN_RE.match(text))


def token_hash(token: str) -> str:
    """The hash that the store keeps for a lease token."""
    return hashlib.sha256(token.encode("ascii")).hexdigest()


def session_login(agent: str, number: int) -> str:
    """The login of the session NUMBER of AGENT, for example "cursor.s2"."""
    return f"{agent}.s{number}"


def iso(value: dt.datetime) -> str:
    return value.astimezone(dt.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


@dataclass(frozen=True)
class Lease:
    token_hash: str
    session: str
    agent: str
    app: str
    folder: str
    pid: int
    handle: str
    started_at: dt.datetime
    last_seen_at: dt.datetime

    def is_live(self, now: dt.datetime) -> bool:
        """True until EXPIRES_SECONDS after the last heartbeat."""
        return now - self.last_seen_at < dt.timedelta(seconds=EXPIRES_SECONDS)

    def seen(self, now: dt.datetime) -> Lease:
        return replace(self, last_seen_at=now)

    def public(self) -> dict:
        return {
            "session": self.session,
            "agent": self.agent,
            "app": self.app,
            "folder": self.folder,
            "pid": self.pid,
            "started_at": iso(self.started_at),
            "last_seen_at": iso(self.last_seen_at),
        }
