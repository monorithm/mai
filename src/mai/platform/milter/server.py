from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import stat

from .envelope import EnvelopeLog
from .from_policy import FromHeaderPolicy
from .protocol import (
    CMD_QUIT,
    RECORD_FAILED_TEXT,
    REPLY_REPLYCODE,
    MilterSession,
    ProtocolError,
    pack,
    read_packet,
)

log = logging.getLogger("mai.milter")


class MilterServer:
    """Serves the milter on a unix socket for Postfix.

    With an envelope log, the milter keeps the envelope of each mail that it
    accepts. When the write fails, the milter gives a temporary failure, so
    that no mail goes out without its envelope record.
    """

    def __init__(
        self,
        socket_path: str,
        policy: FromHeaderPolicy,
        mode: int = 0o660,
        envelopes: EnvelopeLog | None = None,
    ) -> None:
        self._socket_path = socket_path
        self._policy = policy
        self._mode = mode
        self._envelopes = envelopes
        self._server: asyncio.AbstractServer | None = None

    async def _record(self, session: MilterSession, reply: bytes) -> bytes:
        envelope = session.take_envelope()
        if envelope is None or self._envelopes is None:
            return reply
        try:
            await self._envelopes.record(envelope)
        except Exception as exc:  # noqa: BLE001 - each failure gives a tempfail
            log.error("the envelope record failed: %s", type(exc).__name__)
            return pack(REPLY_REPLYCODE, RECORD_FAILED_TEXT.encode() + b"\0")
        return reply

    async def _serve_client(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        session = MilterSession(self._policy)
        try:
            while True:
                packet = await read_packet(reader)
                if packet is None:
                    break
                cmd, data = packet
                if cmd == CMD_QUIT:
                    break
                reply = session.handle(cmd, data)
                if reply is not None:
                    reply = await self._record(session, reply)
                    writer.write(reply)
                    await writer.drain()
                    if reply[4:5] == b"y":
                        log.info(
                            "rejected a message: %s",
                            reply[5:-1].decode(errors="replace"),
                        )
        except ProtocolError as exc:
            log.warning("milter protocol error: %s", exc)
        except (ConnectionError, asyncio.CancelledError):
            pass
        finally:
            writer.close()
            with contextlib.suppress(Exception):
                await writer.wait_closed()

    def _remove_stale_socket(self) -> None:
        with contextlib.suppress(FileNotFoundError):
            if stat.S_ISSOCK(os.lstat(self._socket_path).st_mode):
                os.unlink(self._socket_path)

    async def start(self) -> None:
        self._remove_stale_socket()
        self._server = await asyncio.start_unix_server(
            self._serve_client, path=self._socket_path
        )
        os.chmod(self._socket_path, self._mode)
        log.info("milter listens on %s", self._socket_path)

    async def serve_forever(self) -> None:
        if self._server is None:
            await self.start()
        assert self._server is not None
        try:
            await self._server.serve_forever()
        finally:
            await self.close()

    async def close(self) -> None:
        if self._server is not None:
            self._server.close()
            with contextlib.suppress(Exception):
                await self._server.wait_closed()
            self._server = None
        self._remove_stale_socket()
