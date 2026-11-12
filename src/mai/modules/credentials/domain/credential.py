"""Accounts and credential strings of mai.

A credential is the string "mai1:<login>:<secret>". The secret is 32 random
bytes in lower-case base32 with no padding. The store keeps only a hash.
"""

from __future__ import annotations

import base64
import re
import secrets
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta

from .errors import CredentialError

DOMAIN = "mai.internal"
PREFIX = "mai1"
# psycopg cannot load the value infinity, so a far date stands for no expiry.
FOREVER = datetime(9999, 12, 31, tzinfo=UTC)

SYSTEM_ACCOUNTS = (("owner", "owner"), ("board", "system"))
# Dovecot 2.4 wants the names of the rights, not the letters.
ACL_RIGHTS = (
    "lookup",
    "read",
    "write",
    "write-seen",
    "write-deleted",
    "expunge",
    "insert",
)
# The board account reads each agent queue.
BOARD_ACL_RIGHTS = ("lookup", "read")

# An agent name is the local part of the agent address. It has no dot,
# because a dot separates the agent and the label in a session login.
_AGENT_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,47}$")
_LABEL_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,31}$")
_LOGIN_RE = re.compile(r"^[a-z0-9][a-z0-9.-]{0,63}$")
_SECRET_RE = re.compile(r"[a-z2-7]{20,}")


@dataclass(frozen=True)
class Principal:
    login: str
    kind: str
    status: str
    agent: str | None = None
    label: str | None = None
    password_hash: str | None = None
    expires_at: datetime | None = None
    created_at: datetime | None = None
    last_login_at: datetime | None = None

    def with_changes(self, **changes) -> Principal:
        return replace(self, **changes)

    def is_expired(self, now: datetime) -> bool:
        return self.status == "expired" or (
            self.expires_at is not None and self.expires_at <= now
        )

    def public(self) -> dict:
        return {
            "login": self.login,
            "kind": self.kind,
            "agent": self.agent,
            "label": self.label,
            "status": self.status,
            "expires_at": iso(self.expires_at),
            "created_at": iso(self.created_at),
            "last_login_at": iso(self.last_login_at),
            "address": address(self.login),
        }


def address(login: str) -> str:
    return f"{login}@{DOMAIN}"


def iso(value: datetime | None) -> str | None:
    if value is None:
        return None
    if value.tzinfo is None:
        value = value.replace(tzinfo=UTC)
    if value.year >= 9999:
        return None
    return value.astimezone(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def new_secret() -> str:
    raw = secrets.token_bytes(32)
    return base64.b32encode(raw).decode("ascii").rstrip("=").lower()


def format_credential(login: str, secret: str) -> str:
    return f"{PREFIX}:{login}:{secret}"


def parse_credential(text: object) -> tuple[str, str]:
    """Split "mai1:<login>:<secret>". Raises CredentialError("LOGIN_FAILED")."""
    if not isinstance(text, str):
        raise CredentialError("LOGIN_FAILED")
    parts = text.strip().split(":")
    if len(parts) != 3 or parts[0] != PREFIX:
        raise CredentialError("LOGIN_FAILED")
    login, secret = parts[1], parts[2]
    if login.endswith("@" + DOMAIN):
        login = login[: -len(DOMAIN) - 1]
    if not _LOGIN_RE.match(login) or not _SECRET_RE.fullmatch(secret):
        raise CredentialError("LOGIN_FAILED")
    return login, secret


def check_agent_name(name: object) -> str:
    """Return the name when it is a valid agent name, else raise ValueError."""
    if not isinstance(name, str) or not _AGENT_RE.match(name):
        raise ValueError(f"agent name must match [a-z0-9][a-z0-9-]{{0,47}}: {name!r}")
    if name in {login for login, _kind in SYSTEM_ACCOUNTS}:
        raise ValueError(f"agent name is a system account: {name}")
    return name


def check_label(label: object) -> str:
    if not isinstance(label, str) or not _LABEL_RE.match(label):
        raise ValueError("label must match [a-z0-9][a-z0-9-]{0,31}")
    return label


def parse_expiry(value: object) -> timedelta:
    """Read a lifetime such as 7, "7d", or "12h". A plain number is days."""
    if isinstance(value, str):
        text = value.strip().lower()
        if text.endswith("d"):
            return timedelta(days=int(text[:-1]))
        if text.endswith("h"):
            return timedelta(hours=int(text[:-1]))
        return timedelta(days=int(text))
    if isinstance(value, (int, float)):
        return timedelta(days=float(value))
    raise ValueError("expires must be a number of days or a string like 7d or 12h")
