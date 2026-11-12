"""Reads mail with "doveadm fetch" through the doveadm socket of the container.

The doveadm server allows only the read commands that dovecot.conf lists.
The browser never gets a secret: the app sends each command with -u <user>.
"""

from __future__ import annotations

import json
import shutil
import subprocess

import anyio

from mai.platform.mailserver import doveadm_argv

from ..domain.mail_view import MailboxMissingError, MailReader, MailSummary

FIELDS = "uid date.received hdr.from hdr.subject hdr.message-id"
MAX_SEARCH = 200


class DoveadmMailReader(MailReader):
    async def messages(self, user: str, search: str = "") -> list[MailSummary]:
        query = ["mailbox", "INBOX"]
        term = " ".join(search.split())[:MAX_SEARCH]
        if term:
            query += ["OR", "SUBJECT", term, "FROM", term]
        rows = await anyio.to_thread.run_sync(_fetch, user, FIELDS, query)
        items = [
            MailSummary(
                uid=str(r.get("uid", "")),
                received=str(r.get("date.received", "")),
                sender=str(r.get("hdr.from", "")),
                subject=str(r.get("hdr.subject", "")),
                message_id=str(r.get("hdr.message-id", "")).strip(),
            )
            for r in rows
        ]
        items.sort(key=lambda m: int(m.uid) if m.uid.isdigit() else 0, reverse=True)
        return items

    async def raw(self, user: str, uid: str) -> bytes | None:
        if not uid.isdigit():
            return None
        rows = await anyio.to_thread.run_sync(
            _fetch, user, "text", ["mailbox", "INBOX", "uid", uid]
        )
        if not rows:
            return None
        return str(rows[0].get("text", "")).encode("utf-8", "replace")


def _fetch(user: str, fields: str, query: list[str]) -> list[dict]:
    exe = shutil.which("doveadm")
    if not exe:
        raise MailboxMissingError("doveadm is not available")
    try:
        result = subprocess.run(
            doveadm_argv(
                ("-f", "json", "fetch"), ["-u", user, fields, *query], exe=exe
            ),
            capture_output=True,
            timeout=30,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise MailboxMissingError(type(exc).__name__) from exc
    if result.returncode != 0:
        raise MailboxMissingError(f"doveadm exit {result.returncode}")
    text = result.stdout.decode("utf-8", "replace").strip()
    if not text:
        return []
    data = json.loads(text)
    return [r for r in data if isinstance(r, dict)] if isinstance(data, list) else []
