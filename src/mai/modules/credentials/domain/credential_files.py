"""The port that reads and writes credential files with mode 0600."""

from abc import ABC, abstractmethod


class CredentialFiles(ABC):
    @abstractmethod
    def read(self, path: str) -> str | None:
        """The first line of the file, or None when it cannot be read."""

    @abstractmethod
    def write(self, path: str, credential: str) -> None: ...

    @abstractmethod
    def system_path(self, login: str) -> str:
        """The file of the secret of a system or owner account."""
