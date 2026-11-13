import shutil
import subprocess

import anyio

from mai.platform.contract.time_limit import time_limit_part
from mai.platform.mailserver import doveadm_argv

from ..domain.mailbox_acl import MailboxAcl

# The maximum wait for one doveadm command. A time limit of the caller does
# not wait for the command: the command stops at this limit.
DOVEADM_TIMEOUT_SECONDS = 10


class DoveadmMailboxAcl(MailboxAcl):
    """Runs doveadm acl. On a host with no doveadm each call returns False."""

    async def grant(self, agent: str, login: str, rights: tuple[str, ...]) -> bool:
        with time_limit_part("MAIL_SERVER_DOWN"):
            return await anyio.to_thread.run_sync(
                _run,
                ("acl", "set"),
                ["-u", agent, "INBOX", f"user={login}", *rights],
                abandon_on_cancel=True,
            )

    async def return_unread(self, agent: str, login: str) -> bool:
        with time_limit_part("MAIL_SERVER_DOWN"):
            return await anyio.to_thread.run_sync(
                _run,
                ("move",),
                ["-u", login, f"shared/{agent}/INBOX", "mailbox", "INBOX", "unseen"],
                abandon_on_cancel=True,
            )

    async def remove(self, agent: str, login: str) -> bool:
        with time_limit_part("MAIL_SERVER_DOWN"):
            return await anyio.to_thread.run_sync(
                _run,
                ("acl", "delete"),
                ["-u", agent, "INBOX", f"user={login}"],
                abandon_on_cancel=True,
            )


def _run(command: tuple[str, ...], args: list[str]) -> bool:
    exe = shutil.which("doveadm")
    if not exe:
        return False
    try:
        result = subprocess.run(
            doveadm_argv(command, args, exe=exe),
            capture_output=True,
            timeout=DOVEADM_TIMEOUT_SECONDS,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        return False
    return result.returncode == 0
