import abc


class QueueMover(abc.ABC):
    """Moves one mail of a shared agent queue to the INBOX of a session."""

    @abc.abstractmethod
    def __call__(self, session_login: str, agent: str, uid: int) -> bool: ...
