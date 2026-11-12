import abc


class ServerVersion(abc.ABC):
    """Gives the version of the running server, or None when no source answers."""

    @abc.abstractmethod
    def __call__(self) -> str | None: ...
