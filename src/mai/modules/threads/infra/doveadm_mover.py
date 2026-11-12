"""Moves a mail of a shared agent queue to the INBOX of a session, with doveadm as that session user.

The board account cannot move a mail into the mailbox of a different user
through IMAP. The session has the rights on the shared queue, thus
"doveadm move -u <session> INBOX mailbox shared/<agent>/INBOX uid <uid>" does
the move with the ACL of the session. A shared mailbox has one UID space for
all users, thus the UID that the board account sees is correct for the session.
"""

from __future__ import annotations

import subprocess

from mai.platform.mailserver import doveadm_argv

from ..domain.claim import queue_of
from ..domain.mover import QueueMover


class DoveadmMover(QueueMover):
    def __init__(self) -> None:
        self.run = subprocess.run

    def command(self, session_login: str, agent: str, uid: int) -> list[str]:
        return doveadm_argv(
            ("move",),
            (
                "-u",
                session_login,
                "INBOX",
                "mailbox",
                queue_of(agent),
                "uid",
                str(int(uid)),
            ),
        )

    def __call__(self, session_login: str, agent: str, uid: int) -> bool:
        try:
            result = self.run(
                self.command(session_login, agent, uid), capture_output=True, timeout=30
            )
        except (OSError, subprocess.SubprocessError):
            return False
        return result.returncode == 0
