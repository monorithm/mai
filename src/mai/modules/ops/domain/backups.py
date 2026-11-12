"""The rules of the backup set: names, time stamps, retention, schedule."""

from __future__ import annotations

import datetime as dt
import re

KEEP_BACKUPS = 14
BACKUP_HOUR_UTC = 2

_STAMP_RE = re.compile(r"^mai-(\d{8}T\d{6}Z)\.(pgdump|mail\.tar\.gz)$")
_BARE_STAMP_RE = re.compile(r"^\d{8}T\d{6}Z$")
SUFFIXES = (".pgdump", ".mail.tar.gz")


def stamp_of(now: dt.datetime) -> str:
    """Return the time stamp of a backup made at `now` (UTC)."""
    return now.astimezone(dt.UTC).strftime("%Y%m%dT%H%M%SZ")


def is_stamp(text: str) -> bool:
    return bool(_BARE_STAMP_RE.fullmatch(text))


def stamps_in(names: list[str]) -> list[str]:
    """Return the backup time stamps in a list of file names, newest first."""
    found = {m.group(1) for n in names if (m := _STAMP_RE.match(n))}
    return sorted(found, reverse=True)


def stamps_to_remove(stamps: list[str], keep: int = KEEP_BACKUPS) -> list[str]:
    """Return the stamps outside the newest `keep`."""
    ordered = sorted(set(stamps), reverse=True)
    return ordered[keep:]


def backup_prefix(text: str) -> str:
    """Remove a part suffix, so the result is the common prefix of a backup."""
    for suffix in SUFFIXES:
        if text.endswith(suffix):
            return text[: -len(suffix)]
    return text


def next_backup_at(now: dt.datetime, hour: int = BACKUP_HOUR_UTC) -> dt.datetime:
    """Return the next daily backup time (at `hour`:00 UTC) after `now`."""
    now = now.astimezone(dt.UTC)
    run = now.replace(hour=hour, minute=0, second=0, microsecond=0)
    if run <= now:
        run += dt.timedelta(days=1)
    return run
