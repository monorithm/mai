"""The live state of one MCP session handle.

A handle is "ses-" + 26 base32 characters. Only its SHA-256 is stored. The
open mail session of a handle stays in the memory of this process, because
the secret is never stored.
"""

from __future__ import annotations

import base64
import datetime as dt
import hashlib
import secrets
import threading
from dataclasses import dataclass, field

DOMAIN = "mai.internal"
LEASE = dt.timedelta(hours=12)


@dataclass
class SessionState:
    login: str
    agent: str
    roles: dict
    handle: str
    mail: object
    expires_at: dt.datetime
    kind: str = "session"
    client: str | None = None
    credential_expires_at: dt.datetime | None = None
    fingerprint: str | None = field(default=None, repr=False)
    lock: threading.RLock = field(default_factory=threading.RLock, repr=False)

    @property
    def address(self) -> str:
        return f"{self.login}@{DOMAIN}"

    @property
    def projects(self) -> list:
        return sorted(self.roles)


def handle_sha(handle: str) -> str:
    return hashlib.sha256(handle.encode("utf-8")).hexdigest()


def new_handle() -> str:
    return (
        "ses-"
        + base64.b32encode(secrets.token_bytes(16)).decode("ascii").rstrip("=").lower()
    )


def iso(value: object) -> str | None:
    if value is None:
        return None
    if isinstance(value, str):
        return value
    assert isinstance(value, dt.datetime)
    if value.tzinfo is None:
        value = value.replace(tzinfo=dt.UTC)
    if value.year >= 9999:
        return None
    return value.astimezone(dt.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def login_result(state: SessionState) -> dict:
    """The result of the login tool."""
    return {
        "session": state.handle,
        "login": state.login,
        "address": state.address,
        "agent": state.agent,
        "roles": dict(state.roles),
        "projects": state.projects,
        "expires_at": iso(state.expires_at),
    }
