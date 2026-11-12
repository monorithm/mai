"""The port that gives the unread mail of a thread back to the agent queue."""

from __future__ import annotations

import abc


class ThreadMailReturner(abc.ABC):
    """Moves the unread mail of one thread from the INBOX of a session back to the agent queue.

    A claim that ends (its lease ended, or the session was revoked) leaves the
    replies that the router moved to that session. They must go back to the
    queue, else the next session that claims the thread does not get them.
    """

    @abc.abstractmethod
    def __call__(self, session_login: str, agent: str, root: str) -> bool:
        """Give True when the move ran. A thread with no unread mail also gives True."""
