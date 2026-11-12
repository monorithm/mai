"""The port that counts the mail of a session with no notice mark yet."""

from abc import ABC, abstractmethod

from .session_state import SessionState


class NewMailCounter(ABC):
    @abstractmethod
    async def count(self, state: SessionState) -> int:
        """The count of the mail in the queues of the session with no notice mark."""
