"""Gives the unread mail of a thread back to the agent queue, with doveadm as the session user.

"doveadm move -u <session> shared/<agent>/INBOX mailbox INBOX unseen (
header References <root> OR header In-Reply-To <root> OR header Message-ID <root> )"
moves each unread mail of
the thread with the ACL of the session: the session has the right insert on
the queue of its agent.
"""

from __future__ import annotations

import subprocess

from mai.platform.mailserver import doveadm_argv

from ..domain.claim import queue_of
from ..domain.returner import ThreadMailReturner


class DoveadmReturner(ThreadMailReturner):
    def __init__(self) -> None:
        self.run = subprocess.run

    def command(self, session_login: str, agent: str, root: str) -> list[str]:
        return doveadm_argv(
            ("move",),
            (
                "-u",
                session_login,
                queue_of(agent),
                "mailbox",
                "INBOX",
                "unseen",
                # Dovecot needs parentheses when a query mixes AND and OR.
                "(",
                "header",
                "References",
                root,
                "OR",
                "header",
                "In-Reply-To",
                root,
                "OR",
                "header",
                "Message-ID",
                root,
                ")",
            ),
        )

    def __call__(self, session_login: str, agent: str, root: str) -> bool:
        try:
            result = self.run(
                self.command(session_login, agent, root),
                capture_output=True,
                timeout=30,
                check=False,
            )
        except (OSError, subprocess.SubprocessError):
            return False
        return result.returncode == 0
