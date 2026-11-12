import shutil
import subprocess

import anyio

from mai.platform.mailserver import doveadm_argv

from ..domain.mailbox_acl import MailboxAcl


class DoveadmMailboxAcl(MailboxAcl):
    """Runs doveadm acl. On a host with no doveadm each call returns False."""

    async def grant(self, agent: str, login: str, rights: tuple[str, ...]) -> bool:
        return await anyio.to_thread.run_sync(
            _run, ("acl", "set"), ["-u", agent, "INBOX", f"user={login}", *rights]
        )

    async def return_unread(self, agent: str, login: str) -> bool:
        return await anyio.to_thread.run_sync(
            _run,
            ("move",),
            ["-u", login, f"shared/{agent}/INBOX", "mailbox", "INBOX", "unseen"],
        )

    async def remove(self, agent: str, login: str) -> bool:
        return await anyio.to_thread.run_sync(
            _run, ("acl", "delete"), ["-u", agent, "INBOX", f"user={login}"]
        )


def _run(command: tuple[str, ...], args: list[str]) -> bool:
    exe = shutil.which("doveadm")
    if not exe:
        return False
    try:
        result = subprocess.run(
            doveadm_argv(command, args, exe=exe),
            capture_output=True,
            timeout=30,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        return False
    return result.returncode == 0
