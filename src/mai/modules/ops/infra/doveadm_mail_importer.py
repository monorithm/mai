"""Writes mail into an account INBOX with doveadm, inside the container."""

from __future__ import annotations

import subprocess

import anyio.to_thread
from bustan import Injectable

from mai.platform.mailserver import doveadm_argv

from ..domain.ports import MailImporter


class DoveadmRunner:
    """The sync doveadm calls. The async adapter runs them in a thread."""

    def __init__(self, doveadm: str = "doveadm") -> None:
        self.doveadm = doveadm

    def _run(
        self, command: tuple[str, ...], args: list[str], data: bytes | None = None
    ) -> str:
        r = subprocess.run(
            doveadm_argv(command, args, exe=self.doveadm),
            input=data,
            capture_output=True,
            check=True,
        )
        return r.stdout.decode("utf-8", "replace")

    def find_uid(self, account: str, message_id: str) -> int | None:
        out = self._run(
            ("search",),
            [
                "-u",
                account,
                "mailbox",
                "INBOX",
                "header",
                "Message-ID",
                message_id,
            ],
        )
        for line in out.splitlines():
            parts = line.split()
            if len(parts) >= 2 and parts[1].isdigit():
                return int(parts[1])
        return None

    def append(
        self, account: str, raw: bytes, flags: list[str], message_id: str | None
    ) -> str:
        if message_id and self.find_uid(account, message_id) is not None:
            return "skipped"
        self._run(("save",), ["-u", account, "-m", "INBOX"], data=raw)
        if flags and message_id:
            uid = self.find_uid(account, message_id)
            if uid is not None:
                self._run(
                    ("flags", "add"),
                    [
                        "-u",
                        account,
                        " ".join(flags),
                        "mailbox",
                        "INBOX",
                        "uid",
                        str(uid),
                    ],
                )
        return "imported"


@Injectable()
class DoveadmMailImporter(MailImporter):
    def __init__(self) -> None:
        self._runner = DoveadmRunner()

    async def append(
        self, account: str, raw: bytes, flags: list[str], message_id: str | None
    ) -> str:
        return await anyio.to_thread.run_sync(
            self._runner.append, account, raw, flags, message_id
        )
