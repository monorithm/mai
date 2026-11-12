"""The rules that read a 0.5.0 Maildir: flags, target accounts, and header renames."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path

# Maildir info flags that map to IMAP flags. The trashed flag T is not
# imported, because it would mark the copy for expunge.
FLAG_MAP = {
    "S": "\\Seen",
    "R": "\\Answered",
    "F": "\\Flagged",
    "D": "\\Draft",
}
# A lower-case letter is a Dovecot keyword; 0.5.0 used "n" for notified.
KEYWORD_MAP = {"n": "$MaiNotified"}


@dataclass
class OldMail:
    """One mail file of the old Maildir."""

    address: str  # the folder name in the old Maildir
    account: str  # the account that gets the mail: the same name as the folder
    path: Path
    subdir: str  # "cur" or "new"
    flags: list[str] = field(default_factory=list)
    message_id: str | None = None


def parse_info_flags(filename: str) -> list[str]:
    """Map the info part of a Maildir file name to IMAP flags."""
    if ":2," not in filename:
        return []
    info = filename.rsplit(":2,", 1)[1]
    out: list[str] = []
    for ch in info:
        flag = FLAG_MAP.get(ch) or KEYWORD_MAP.get(ch)
        if flag and flag not in out:
            out.append(flag)
    return out


_PREFIX_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9-]*$")


def parse_header_rename(text: str) -> tuple[str, str]:
    """Read "OLD=NEW", for example "X-Old-=X-Mai-". Raises ValueError for a bad value."""
    old, sep, new = text.partition("=")
    if not sep or not _PREFIX_RE.match(old) or not _PREFIX_RE.match(new):
        raise ValueError(
            f"a header rename must be OLD=NEW with header name characters: {text!r}"
        )
    return old, new


class HeaderRenamer:
    """Changes the prefix of header names in the header block of a mail.

    The match of OLD is not case-sensitive and starts at the line start. The
    body does not change. With no renames, the mail does not change.
    """

    def __init__(self, renames: list[tuple[str, str]] | None = None) -> None:
        self._rules = [
            (
                re.compile(rb"(?im)^" + re.escape(old.encode("ascii"))),
                new.encode("ascii"),
            )
            for old, new in renames or []
        ]

    def __call__(self, raw: bytes) -> bytes:
        if not self._rules:
            return raw
        head, body = raw, b""
        for sep in (b"\r\n\r\n", b"\n\n"):
            i = raw.find(sep)
            if i >= 0:
                head, body = raw[:i], raw[i:]
                break
        for pattern, new in self._rules:
            head = pattern.sub(lambda _m, new=new: new, head)
        return head + body
