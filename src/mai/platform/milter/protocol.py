"""The Sendmail milter protocol, version 6, as Postfix speaks it.

Each packet is a 4 byte big-endian length, then a command byte, then the
data. The length counts the command byte and the data.
"""

from __future__ import annotations

import asyncio
import struct

from .from_policy import FromHeaderPolicy

VERSION = 6
MAX_PACKET = 1024 * 1024

# Commands from the MTA.
CMD_ABORT = b"A"
CMD_BODY = b"B"
CMD_CONNECT = b"C"
CMD_MACRO = b"D"
CMD_BODYEOB = b"E"
CMD_HELO = b"H"
CMD_QUIT_NC = b"K"
CMD_HEADER = b"L"
CMD_MAIL = b"M"
CMD_EOH = b"N"
CMD_OPTNEG = b"O"
CMD_QUIT = b"Q"
CMD_RCPT = b"R"
CMD_DATA = b"T"
CMD_UNKNOWN = b"U"

# Replies to the MTA.
REPLY_ACCEPT = b"a"
REPLY_CONTINUE = b"c"
REPLY_TEMPFAIL = b"t"
REPLY_REPLYCODE = b"y"
REPLY_OPTNEG = b"O"

# Protocol flags: the events that this milter does not want to receive.
SMFIP_NOCONNECT = 0x01
SMFIP_NOHELO = 0x02
SMFIP_NORCPT = 0x08
SMFIP_NOBODY = 0x10
SMFIP_NOUNKNOWN = 0x100
SMFIP_NODATA = 0x200
WANTED_SKIPS = (
    SMFIP_NOCONNECT
    | SMFIP_NOHELO
    | SMFIP_NORCPT
    | SMFIP_NOBODY
    | SMFIP_NOUNKNOWN
    | SMFIP_NODATA
)

REJECT_TEXT = "550 5.7.1 From header rejected: {reason}"


class ProtocolError(Exception):
    pass


def pack(cmd: bytes, data: bytes = b"") -> bytes:
    return struct.pack(">I", len(data) + 1) + cmd + data


def split_nul(data: bytes) -> list[str]:
    parts = data.split(b"\0")
    if parts and parts[-1] == b"":
        parts.pop()
    return [p.decode("utf-8", "replace") for p in parts]


async def read_packet(reader: asyncio.StreamReader) -> tuple[bytes, bytes] | None:
    """Reads one packet. Returns None at the end of the stream."""
    try:
        head = await reader.readexactly(4)
    except asyncio.IncompleteReadError as exc:
        if exc.partial:
            raise ProtocolError("truncated packet length") from exc
        return None
    (length,) = struct.unpack(">I", head)
    if length < 1 or length > MAX_PACKET:
        raise ProtocolError(f"bad packet length {length}")
    try:
        body = await reader.readexactly(length)
    except asyncio.IncompleteReadError as exc:
        raise ProtocolError("truncated packet") from exc
    return body[:1], body[1:]


class MilterSession:
    """The state of one milter connection from Postfix.

    One connection can carry more than one message. The state of a message
    is reset at each MAIL FROM, at abort and at the end of the message.
    """

    def __init__(self, policy: FromHeaderPolicy) -> None:
        self._policy = policy
        self._macros: dict[bytes, dict[str, str]] = {}
        self._reset_message()

    def _reset_message(self) -> None:
        self._login: str | None = None
        self._from_values: list[str] = []
        self._macros.pop(CMD_MAIL, None)

    def _macro(self, name: str) -> str | None:
        for scope in (CMD_MAIL, CMD_CONNECT, CMD_HELO):
            values = self._macros.get(scope, {})
            for key in (name, "{" + name + "}"):
                if values.get(key):
                    return values[key]
        return None

    def handle(self, cmd: bytes, data: bytes) -> bytes | None:
        """Returns the reply packet, or None when the command needs no reply."""
        if cmd == CMD_OPTNEG:
            if len(data) < 12:
                raise ProtocolError("short option negotiation")
            _, mta_actions, mta_protocol = struct.unpack(">III", data[:12])
            del mta_actions
            protocol = WANTED_SKIPS & mta_protocol
            return pack(REPLY_OPTNEG, struct.pack(">III", VERSION, 0, protocol))
        if cmd == CMD_MACRO:
            if not data:
                return None
            fields = split_nul(data[1:])
            self._macros[data[:1]] = dict(zip(fields[0::2], fields[1::2], strict=False))
            return None
        if cmd == CMD_MAIL:
            mail_macros = self._macros.get(CMD_MAIL)
            self._reset_message()
            if mail_macros is not None:
                self._macros[CMD_MAIL] = mail_macros
            self._login = self._macro("auth_authen")
            return pack(REPLY_CONTINUE)
        if cmd == CMD_HEADER:
            fields = split_nul(data)
            name = fields[0] if fields else ""
            value = fields[1] if len(fields) > 1 else ""
            if name.strip().lower() == "from":
                self._from_values.append(value)
            return pack(REPLY_CONTINUE)
        if cmd == CMD_EOH:
            reason = self._policy.check(self._login, self._from_values)
            if reason is not None:
                text = REJECT_TEXT.format(reason=reason)
                return pack(REPLY_REPLYCODE, text.encode() + b"\0")
            return pack(REPLY_CONTINUE)
        if cmd == CMD_BODYEOB:
            self._reset_message()
            return pack(REPLY_ACCEPT)
        if cmd == CMD_ABORT:
            self._reset_message()
            return None
        if cmd == CMD_QUIT_NC:
            self._macros.clear()
            self._reset_message()
            return None
        if cmd in (CMD_CONNECT, CMD_HELO, CMD_RCPT, CMD_DATA, CMD_BODY, CMD_UNKNOWN):
            return pack(REPLY_CONTINUE)
        raise ProtocolError(f"unknown command {cmd!r}")
