"""The credential file format: one line "mai1:<login>:<secret>"."""

from __future__ import annotations

import re
from pathlib import Path

from mai.platform.contract import ToolError

from ..domain.credentials import CredentialReader

_PREFIX = "mai1"
_LOGIN = re.compile(r"^[a-z0-9][a-z0-9.-]{0,63}$")


def parse_credential(text: str) -> tuple[str, str]:
    """Split "mai1:<login>:<secret>" into (login, secret). Raises ToolError(LOGIN_FAILED)."""
    parts = (text or "").strip().split(":")
    if len(parts) != 3 or parts[0] != _PREFIX:
        raise ToolError("LOGIN_FAILED")
    login, secret = parts[1], parts[2]
    login = login.removesuffix("@mai.internal")
    if not _LOGIN.match(login) or not re.fullmatch(r"[a-z2-7]{20,}", secret):
        raise ToolError("LOGIN_FAILED")
    return login, secret


def read_credential_file(path: str) -> tuple[str, str]:
    try:
        text = Path(path).expanduser().read_text(encoding="utf-8")
    except OSError:
        raise ToolError("LOGIN_FAILED") from None
    return parse_credential(text)


class FileCredentialReader(CredentialReader):
    def read(self, path: str) -> tuple[str, str]:
        return read_credential_file(path)

    def parse(self, text: str) -> tuple[str, str]:
        return parse_credential(text)
