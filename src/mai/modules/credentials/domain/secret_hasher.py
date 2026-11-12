"""The port that hashes and checks secrets in the form that Dovecot reads."""

from abc import ABC, abstractmethod


class SecretHasher(ABC):
    @abstractmethod
    def hash(self, secret: str) -> str: ...

    @abstractmethod
    def verify(self, secret: str, stored: str | None) -> bool: ...
