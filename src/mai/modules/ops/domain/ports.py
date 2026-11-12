"""The ports of the ops module."""

from __future__ import annotations

from abc import ABC, abstractmethod
from contextlib import AbstractContextManager, nullcontext
from pathlib import Path

from .maildir import OldMail


class MailImporter(ABC):
    """Writes one old mail into the INBOX of an account."""

    @abstractmethod
    async def append(
        self, account: str, raw: bytes, flags: list[str], message_id: str | None
    ) -> str:
        """Return "imported" or "skipped" (the Message-ID is already there)."""


class AccountDirectory(ABC):
    """Tells which accounts exist and makes an agent account for the import."""

    @abstractmethod
    async def exists(self, account: str) -> bool: ...

    @abstractmethod
    async def add_agent(self, name: str) -> None:
        """Make the agent account NAME. Raises ValueError for a bad name."""


class MaildirSource(ABC):
    """Reads the mails of a 0.5.0 Maildir root. It never changes the source."""

    @abstractmethod
    async def scan(self, root: Path) -> list[OldMail]: ...

    @abstractmethod
    async def read(self, mail: OldMail) -> bytes: ...


class BackupTool(ABC):
    """Makes and restores the two parts of a backup."""

    @abstractmethod
    async def dump_database(self, target: Path) -> None: ...

    @abstractmethod
    async def restore_database(self, source: Path) -> None: ...

    @abstractmethod
    async def stop_daemons(self) -> None: ...

    @abstractmethod
    async def start_daemons(self) -> None: ...

    def exclusive(self) -> AbstractContextManager:
        """A context in which no restore runs. The default has no lock."""
        return nullcontext()
