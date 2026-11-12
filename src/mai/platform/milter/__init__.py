"""A Postfix milter for mai.

It makes the From header agree with the SASL login, refuses the copies
mailbox as a recipient, and keeps the envelope of each accepted mail.
"""

from .envelope import Envelope, EnvelopeLog, MemoryEnvelopeLog, PostgresEnvelopeLog
from .from_policy import FromHeaderPolicy
from .protocol import MilterSession, ProtocolError
from .recipient_policy import CopiesRecipientPolicy
from .server import MilterServer

__all__ = [
    "CopiesRecipientPolicy",
    "Envelope",
    "EnvelopeLog",
    "FromHeaderPolicy",
    "MemoryEnvelopeLog",
    "MilterServer",
    "MilterSession",
    "PostgresEnvelopeLog",
    "ProtocolError",
]
