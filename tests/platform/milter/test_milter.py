"""Tests the From header milter with a fake Postfix client on a unix socket."""

from __future__ import annotations

import asyncio
import struct
import tempfile
from pathlib import Path

import pytest

from mai.platform.milter import FromHeaderPolicy, MilterServer, MilterSession
from mai.platform.milter.protocol import read_packet

ALL_PROTOCOL = 0x1FFFFF


def pkt(cmd: bytes, data: bytes = b"") -> bytes:
    return struct.pack(">I", len(data) + 1) + cmd + data


def macros(scope: bytes, **pairs: str) -> bytes:
    data = scope + b"".join(
        k.encode() + b"\0" + v.encode() + b"\0" for k, v in pairs.items()
    )
    return pkt(b"D", data)


def header(name: str, value: str) -> bytes:
    return pkt(b"L", name.encode() + b"\0" + value.encode() + b"\0")


class FakePostfix:
    def __init__(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        self.reader = reader
        self.writer = writer

    async def send(
        self, data: bytes, expect_reply: bool = True
    ) -> tuple[bytes, bytes] | None:
        self.writer.write(data)
        await self.writer.drain()
        if not expect_reply:
            return None
        return await asyncio.wait_for(read_packet(self.reader), 5)

    async def negotiate(self) -> tuple[int, int, int]:
        reply = await self.send(pkt(b"O", struct.pack(">III", 6, 0x1FF, ALL_PROTOCOL)))
        assert reply is not None and reply[0] == b"O"
        return struct.unpack(">III", reply[1][:12])

    async def message(
        self, login: str | None, from_values: list[str]
    ) -> tuple[bytes, bytes]:
        if login is None:
            await self.send(macros(b"M", i="ABC"), expect_reply=False)
        else:
            await self.send(
                macros(b"M", **{"{auth_authen}": login}), expect_reply=False
            )
        reply = await self.send(pkt(b"M", b"<x@mai.internal>\0"))
        assert reply == (b"c", b"")
        for value in from_values:
            assert await self.send(header("From", value)) == (b"c", b"")
        assert await self.send(header("Subject", "hello")) == (b"c", b"")
        reply = await self.send(pkt(b"N"))
        assert reply is not None
        if reply[0] == b"c":
            final = await self.send(pkt(b"E"))
            assert final == (b"a", b"")
        else:
            await self.send(pkt(b"A"), expect_reply=False)
        return reply


@pytest.fixture
def socket_path():
    with tempfile.TemporaryDirectory(dir="/tmp") as tmp:
        yield str(Path(tmp) / "milter.sock")


async def _with_client(socket_path: str, body) -> None:
    server = MilterServer(socket_path, FromHeaderPolicy("mai.internal"))
    await server.start()
    try:
        reader, writer = await asyncio.open_unix_connection(socket_path)
        try:
            await body(FakePostfix(reader, writer))
        finally:
            writer.close()
    finally:
        await server.close()
    assert not Path(socket_path).exists()


def run(socket_path, body) -> None:
    asyncio.run(_with_client(socket_path, body))


def test_negotiation_asks_v6_and_skips_unused_events(socket_path):
    async def body(pf: FakePostfix) -> None:
        version, actions, protocol = await pf.negotiate()
        assert version == 6
        assert actions == 0
        assert protocol & 0x01 and protocol & 0x02 and protocol & 0x10
        assert not protocol & 0x20  # headers are wanted
        assert not protocol & 0x04  # MAIL FROM is wanted

    run(socket_path, body)


@pytest.mark.parametrize(
    "value",
    [
        "cursor@mai.internal",
        "Cursor Agent <cursor@mai.internal>",
        "CURSOR@Mai.Internal",
    ],
)
def test_matching_from_is_accepted(socket_path, value):
    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        assert await pf.message("cursor", [value]) == (b"c", b"")

    run(socket_path, body)


@pytest.mark.parametrize(
    ("login", "values", "reason"),
    [
        ("cursor", ["owner@mai.internal"], b"does not match"),
        ("cursor", ["Cursor <owner@mai.internal>"], b"does not match"),
        ("cursor", ["cursor@mai.internal, owner@mai.internal"], b"exactly 1 address"),
        ("cursor", [], b"0 From headers"),
        ("cursor", ["cursor@mai.internal", "cursor@mai.internal"], b"2 From headers"),
        (None, ["cursor@mai.internal"], b"no authenticated login"),
        ("cursor", ["cursor@mai.internal.evil.example"], b"does not match"),
    ],
)
def test_forged_or_ambiguous_from_is_rejected(socket_path, login, values, reason):
    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        cmd, data = await pf.message(login, values)
        assert cmd == b"y"
        assert data.startswith(b"550 5.7.1 ")
        assert data.endswith(b"\0")
        assert reason in data

    run(socket_path, body)


def test_state_resets_between_messages_on_one_connection(socket_path):
    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        assert (await pf.message("cursor", ["owner@mai.internal"]))[0] == b"y"
        assert await pf.message("owner", ["owner@mai.internal"]) == (b"c", b"")
        assert (await pf.message(None, ["owner@mai.internal"]))[0] == b"y"
        await pf.send(pkt(b"K"), expect_reply=False)
        await pf.negotiate()
        assert await pf.message("cursor", ["cursor@mai.internal"]) == (b"c", b"")
        await pf.send(pkt(b"Q"), expect_reply=False)

    run(socket_path, body)


def test_other_events_get_continue():
    session = MilterSession(FromHeaderPolicy())
    for cmd in (b"C", b"H", b"R", b"T", b"B", b"U"):
        assert session.handle(cmd, b"x\0") == pkt(b"c")
    assert session.handle(b"D", b"") is None
    assert session.handle(b"A", b"") is None


def test_folded_from_header_is_unfolded():
    policy = FromHeaderPolicy()
    assert policy.check("cursor", ['"Cursor\r\n Agent" <cursor@mai.internal>']) is None
