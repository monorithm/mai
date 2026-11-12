"""Tests the copies rule and the envelope record of the milter."""

from __future__ import annotations

import asyncio
import tempfile
from pathlib import Path

import pytest

from mai.platform.milter import (
    CopiesRecipientPolicy,
    FromHeaderPolicy,
    MemoryEnvelopeLog,
    MilterServer,
    MilterSession,
)

from .test_milter import FakePostfix, header, macros, pkt


@pytest.fixture
def socket_path():
    with tempfile.TemporaryDirectory(dir="/tmp") as tmp:
        yield str(Path(tmp) / "milter.sock")


async def _with_client(socket_path: str, log: MemoryEnvelopeLog, body) -> None:
    server = MilterServer(socket_path, FromHeaderPolicy("mai.internal"), envelopes=log)
    await server.start()
    try:
        reader, writer = await asyncio.open_unix_connection(socket_path)
        try:
            await body(FakePostfix(reader, writer))
        finally:
            writer.close()
    finally:
        await server.close()


def run(socket_path: str, log: MemoryEnvelopeLog, body) -> None:
    asyncio.run(_with_client(socket_path, log, body))


async def send_mail(
    pf: FakePostfix, login: str, recipients: list[str], message_id: str | None
) -> list[tuple[bytes, bytes] | None]:
    """Send one mail. Return the reply to each RCPT, then the final reply."""
    await pf.send(macros(b"M", i="4AB12C", **{"{auth_authen}": login}), False)
    reply = await pf.send(pkt(b"M", f"<{login}@mai.internal>\0".encode()))
    assert reply == (b"c", b"")
    replies: list[tuple[bytes, bytes] | None] = []
    for rcpt in recipients:
        await pf.send(macros(b"R", i="4AB12C"), False)
        replies.append(await pf.send(pkt(b"R", f"<{rcpt}>\0".encode())))
    assert await pf.send(header("From", f"{login}@mai.internal")) == (b"c", b"")
    if message_id is not None:
        assert await pf.send(header("Message-ID", message_id)) == (b"c", b"")
    assert await pf.send(pkt(b"N")) == (b"c", b"")
    await pf.send(macros(b"E", i="4AB12C"), False)
    replies.append(await pf.send(pkt(b"E")))
    return replies


@pytest.mark.parametrize(
    "address",
    [
        "copies@mai.internal",
        "<copies@mai.internal>",
        "<COPIES@Mai.Internal>",
        "<copies>",
        '<"copies"@mai.internal>',
        "<@relay.example:copies@mai.internal>",
        "<copies@mai.internal.>",
    ],
)
def test_copies_address_is_refused(address):
    assert CopiesRecipientPolicy().check(address) is not None


@pytest.mark.parametrize(
    "address",
    [
        "<beta.s1@mai.internal>",
        "<copies@other.example>",
        "<copies-x@mai.internal>",
        "<owner@mai.internal>",
    ],
)
def test_other_addresses_are_permitted(address):
    assert CopiesRecipientPolicy().check(address) is None


def test_rcpt_copies_gets_550_and_is_not_recorded(socket_path):
    log = MemoryEnvelopeLog()

    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        replies = await send_mail(
            pf, "alpha.s1", ["beta@mai.internal", "copies@mai.internal"], "<m1@x>"
        )
        assert replies[0] == (b"c", b"")
        cmd, data = replies[1] or (b"", b"")
        assert cmd == b"y"
        assert data.startswith(b"550 5.7.1 ")
        assert b"copies" in data
        assert replies[2] == (b"a", b"")

    run(socket_path, log, body)
    assert len(log.envelopes) == 1
    assert log.envelopes[0].recipients == ["beta@mai.internal"]


def test_each_accepted_mail_has_its_envelope(socket_path):
    log = MemoryEnvelopeLog()

    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        first = await send_mail(
            pf,
            "alpha.s1",
            ["beta@mai.internal", "gamma@mai.internal", "owner@mai.internal"],
            " <m1@mai.internal>",
        )
        assert first[-1] == (b"a", b"")
        second = await send_mail(pf, "beta.s2", ["alpha@mai.internal"], None)
        assert second[-1] == (b"a", b"")

    run(socket_path, log, body)
    assert len(log.envelopes) == 2
    one, two = log.envelopes
    assert one.queue_id == "4AB12C"
    assert one.message_id == "<m1@mai.internal>"
    assert one.sender == "alpha.s1@mai.internal"
    assert one.recipients == [
        "beta@mai.internal",
        "gamma@mai.internal",
        "owner@mai.internal",
    ]
    assert two.sender == "beta.s2@mai.internal"
    assert two.message_id is None
    assert two.recipients == ["alpha@mai.internal"]


def test_rejected_mail_has_no_envelope(socket_path):
    log = MemoryEnvelopeLog()

    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        cmd, _data = await pf.message("cursor", ["owner@mai.internal"])
        assert cmd == b"y"

    run(socket_path, log, body)
    assert log.envelopes == []


def test_failed_record_gives_a_temporary_failure(socket_path):
    log = MemoryEnvelopeLog(fail=True)

    async def body(pf: FakePostfix) -> None:
        await pf.negotiate()
        replies = await send_mail(pf, "alpha.s1", ["beta@mai.internal"], "<m@x>")
        cmd, data = replies[-1] or (b"", b"")
        assert cmd == b"y"
        assert data.startswith(b"451 4.3.0 ")

    run(socket_path, log, body)


def test_negotiation_wants_rcpt(socket_path):
    async def body(pf: FakePostfix) -> None:
        _version, _actions, protocol = await pf.negotiate()
        assert not protocol & 0x08

    run(socket_path, MemoryEnvelopeLog(), body)


def test_abort_drops_the_envelope():
    session = MilterSession(FromHeaderPolicy())
    session.handle(b"M", b"<a@mai.internal>\0")
    session.handle(b"R", b"<b@mai.internal>\0")
    session.handle(b"A", b"")
    assert session.take_envelope() is None
    session.handle(b"M", b"<a@mai.internal>\0")
    session.handle(b"E", b"")
    envelope = session.take_envelope()
    assert envelope is not None and envelope.recipients == []
    assert session.take_envelope() is None
