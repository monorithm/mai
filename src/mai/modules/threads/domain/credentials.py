import abc


class CredentialReader(abc.ABC):
    """Reads a credential: a file path, or "-" for one line of stdin. Gives (login, secret)."""

    @abc.abstractmethod
    def read(self, path: str) -> tuple[str, str]: ...

    @abc.abstractmethod
    def parse(self, text: str) -> tuple[str, str]: ...
