import datetime as dt

import pytest

from mai.modules.threads.application.claim_service import ClaimService
from mai.modules.threads.infra.in_memory_claim_store import InMemoryClaimStore
from mai.platform.contract import ToolError
from mai.platform.mailserver import ref_of, thread_ref_of

from .fakes import OTHER, REPLY, ROOT, FakeMail, RecordingReturner, State, queue

pytestmark = pytest.mark.anyio


@pytest.fixture
def anyio_backend():
    return "asyncio"


async def test_claim_moves_thread_and_is_idempotent():
    mail = FakeMail(queue())
    service = ClaimService(InMemoryClaimStore(), RecordingReturner())
    a = State("cursor.lane-a", mail)
    out = await service.claim(a, ref=ref_of(ROOT))
    assert out["thread_ref"] == thread_ref_of(ROOT)
    assert sorted(out["moved"]) == sorted([ref_of(ROOT), ref_of(REPLY)])
    assert list(mail.boxes["shared/cursor/INBOX"]) == [3]
    again = await service.claim(a, thread_ref=thread_ref_of(ROOT))
    assert again["moved"] == [] and again["owner"] == "cursor.lane-a"
    assert await service.holder("cursor", ROOT) == "cursor.lane-a"


async def test_claim_by_other_session_is_refused():
    mail = FakeMail(queue())
    service = ClaimService(InMemoryClaimStore(), RecordingReturner())
    await service.claim(State("cursor.lane-a", mail), ref=ref_of(ROOT))
    with pytest.raises(ToolError) as err:
        await service.claim(
            State("cursor.lane-b", mail), thread_ref=thread_ref_of(ROOT)
        )
    assert err.value.code == "THREAD_CLAIMED"


async def test_release_returns_unread_and_ends_lease():
    mail = FakeMail(queue())
    store = InMemoryClaimStore()
    service = ClaimService(store, RecordingReturner())
    a = State("cursor.lane-a", mail)
    await service.claim(a, ref=ref_of(ROOT))
    out = await service.release(a, thread_ref_of(ROOT))
    assert out["released"] is True and len(out["returned"]) == 2
    assert await service.holder("cursor", ROOT) is None
    assert (await service.release(a, thread_ref_of(ROOT)))["released"] is False
    # A different session can claim the thread after the release.
    assert (await service.claim(State("cursor.lane-b", mail), ref=ref_of(OTHER)))[
        "owner"
    ] == "cursor.lane-b"


async def test_invalid_and_unknown_refs():
    service = ClaimService(InMemoryClaimStore(), RecordingReturner())
    a = State("cursor.lane-a", FakeMail(queue()))
    for kwargs, path in (
        ({"ref": "bad"}, "ref"),
        ({}, "thread_ref"),
        ({"thread_ref": "x"}, "thread_ref"),
    ):
        with pytest.raises(ToolError) as err:
            await service.claim(a, **kwargs)
        assert err.value.code == "INVALID_ARGUMENT" and err.value.path == path
    with pytest.raises(ToolError) as err:
        await service.claim(a, ref="ref-0000000000000000")
    assert err.value.code == "UNKNOWN_MESSAGE"


async def test_lease_end_is_found_once():
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    service = ClaimService(store, RecordingReturner())
    await service.claim(State("cursor.lane-a", FakeMail(queue())), ref=ref_of(ROOT))
    assert await store.ended_unsignalled() == []
    clock[0] += dt.timedelta(minutes=31)
    [claim] = await store.ended_unsignalled()
    assert await store.mark_signalled(claim) is True
    assert await store.mark_signalled(claim) is False
    assert await store.ended_unsignalled() == []


async def test_a_takeover_of_an_ended_claim_first_returns_the_unread_mail_of_the_old_holder():
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    returner = RecordingReturner()
    service = ClaimService(store, returner)
    boxes = queue()
    await service.claim(State("cursor.lane-a", FakeMail(boxes)), ref=ref_of(ROOT))
    assert returner.calls == []
    clock[0] += dt.timedelta(minutes=31)
    result = await service.claim(
        State("cursor.lane-b", FakeMail(boxes)), thread_ref=thread_ref_of(ROOT)
    )
    assert result["owner"] == "cursor.lane-b"
    assert returner.calls == [("cursor.lane-a", "cursor", ROOT)]


async def test_a_takeover_stops_when_the_return_of_the_old_holder_fails():
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    returner = RecordingReturner(ok=False)
    service = ClaimService(store, returner)
    boxes = queue()
    await service.claim(State("cursor.lane-a", FakeMail(boxes)), ref=ref_of(ROOT))
    clock[0] += dt.timedelta(minutes=31)
    with pytest.raises(ToolError) as err:
        await service.claim(
            State("cursor.lane-b", FakeMail(boxes)), thread_ref=thread_ref_of(ROOT)
        )
    assert err.value.code == "MAIL_SERVER_DOWN"
    assert await store.live_holder("cursor", ROOT) is None


async def test_a_takeover_from_a_revoked_holder_goes_on_when_the_return_fails():
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    service = ClaimService(store, RecordingReturner(ok=False))
    boxes = queue()
    await service.claim(State("cursor.lane-a", FakeMail(boxes)), ref=ref_of(ROOT))
    clock[0] += dt.timedelta(minutes=31)
    store.dead_sessions.add("cursor.lane-a")
    result = await service.claim(
        State("cursor.lane-b", FakeMail(boxes)), thread_ref=thread_ref_of(ROOT)
    )
    assert result["owner"] == "cursor.lane-b"
