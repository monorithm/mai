"""The port that gives a login rights on the INBOX of an agent queue."""

from abc import ABC, abstractmethod


class MailboxAcl(ABC):
    @abstractmethod
    async def grant(self, agent: str, login: str, rights: tuple[str, ...]) -> bool:
        """Returns False when the mail server cannot set the rights. The caller does not stop on False."""

    @abstractmethod
    async def remove(self, agent: str, login: str) -> bool: ...

    async def return_unread(self, agent: str, login: str) -> bool:
        """Move the unread mail of the session INBOX back to the agent queue.

        A revoke calls it before it removes the rights of the session, because
        the move needs the right insert on the queue. The default does nothing.
        """
        return True
