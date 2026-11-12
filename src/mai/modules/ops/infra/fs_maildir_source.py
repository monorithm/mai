"""Reads a 0.5.0 Maildir root from the file system, read only."""

from __future__ import annotations

import email.parser
import email.policy
from pathlib import Path

import anyio.to_thread
from bustan import Injectable

from ..domain.maildir import OldMail, parse_info_flags
from ..domain.ports import MaildirSource


def _message_id(path: Path) -> str | None:
    with open(path, "rb") as fh:
        head = fh.read(65536)
    sep = head.find(b"\r\n\r\n")
    if sep < 0:
        sep = head.find(b"\n\n")
    if sep >= 0:
        head = head[:sep]
    msg = email.parser.BytesHeaderParser(policy=email.policy.compat32).parsebytes(head)
    mid = msg.get("Message-ID")
    return str(mid).strip() if mid else None


def scan_maildir(root: Path) -> list[OldMail]:
    """List the mails of a Maildir root: one folder per address, each with cur and new."""
    rootp = Path(root).expanduser()
    if not rootp.is_dir():
        raise FileNotFoundError(f"not a directory: {rootp}")
    mails: list[OldMail] = []
    for folder in sorted(rootp.iterdir()):
        if not folder.is_dir() or folder.name.startswith("."):
            continue
        if not ((folder / "cur").is_dir() or (folder / "new").is_dir()):
            continue
        for sub in ("new", "cur"):
            d = folder / sub
            if not d.is_dir():
                continue
            for f in sorted(d.iterdir()):
                if not f.is_file() or f.name.startswith("."):
                    continue
                mails.append(
                    OldMail(
                        address=folder.name,
                        account=folder.name,
                        path=f,
                        subdir=sub,
                        flags=parse_info_flags(f.name),
                        message_id=_message_id(f),
                    )
                )
    return mails


@Injectable()
class FsMaildirSource(MaildirSource):
    async def scan(self, root: Path) -> list[OldMail]:
        return await anyio.to_thread.run_sync(scan_maildir, root)

    async def read(self, mail: OldMail) -> bytes:
        return await anyio.to_thread.run_sync(mail.path.read_bytes)
