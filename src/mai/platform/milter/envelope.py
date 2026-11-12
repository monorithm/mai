"""The envelope of a mail, and the log that keeps it for the owner.

The copy of a mail in the mailbox "copies" holds the headers only. A Bcc
recipient is not in the headers, thus the milter keeps the envelope: the
queue ID, the Message-ID, the sender, and each recipient.
"""

from __future__ import annotations

import asyncio
from abc import ABC, abstractmethod
from dataclasses import dataclass, field

DEFAULT_DSN = "host=/run/postgresql dbname=mai user=postfix"

INSERT_SQL = (
    "INSERT INTO mail_auth.envelope (queue_id, message_id, sender, recipients) "
    "VALUES (%s, %s, %s, %s)"
)


def bare_address(value: str) -> str:
    """Return the address of a MAIL FROM or RCPT TO argument, with no brackets.

    The argument can have a source route ("<@a,@b:user@domain>"). The result
    keeps the case of the address.
    """
    text = value.strip()
    if text.startswith("<"):
        end = text.find(">")
        text = text[1:end] if end >= 0 else text[1:]
    if text.startswith("@") and ":" in text:
        text = text.split(":", 1)[1]
    return text.strip()


@dataclass
class Envelope:
    """The envelope of one mail, as the SMTP client gave it."""

    queue_id: str | None = None
    message_id: str | None = None
    sender: str = ""
    recipients: list[str] = field(default_factory=list)


class EnvelopeLog(ABC):
    """The port that keeps the envelope of each accepted mail."""

    @abstractmethod
    async def record(self, envelope: Envelope) -> None:
        """Keep the envelope. Raise an exception when the write fails."""


class MemoryEnvelopeLog(EnvelopeLog):
    """Keeps the envelopes in a list. The tests use it."""

    def __init__(self, fail: bool = False) -> None:
        self.envelopes: list[Envelope] = []
        self.fail = fail

    async def record(self, envelope: Envelope) -> None:
        if self.fail:
            raise OSError("the envelope log is not available")
        self.envelopes.append(envelope)


class PostgresEnvelopeLog(EnvelopeLog):
    """Writes each envelope to the table mail_auth.envelope.

    The milter runs as the OS user postfix, thus it connects as the role
    postfix over the Unix socket. The role has the right INSERT on the table
    and no other right. Each write opens a short connection in a thread, so
    that a stopped database cannot block the loop of the milter.
    """

    def __init__(self, dsn: str = DEFAULT_DSN, timeout: float = 10.0) -> None:
        self._dsn = dsn
        self._timeout = timeout

    async def record(self, envelope: Envelope) -> None:
        await asyncio.wait_for(
            asyncio.to_thread(self._write, envelope), timeout=self._timeout
        )

    def _write(self, envelope: Envelope) -> None:
        import psycopg

        with psycopg.connect(
            self._dsn, connect_timeout=int(self._timeout), autocommit=True
        ) as conn:
            conn.execute(
                INSERT_SQL,
                (
                    envelope.queue_id,
                    envelope.message_id,
                    envelope.sender,
                    list(envelope.recipients),
                ),
            )
