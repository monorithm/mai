import datetime as dt
from typing import cast

import pytest

from mai.modules.threads.application.claim_service import ClaimService
from mai.modules.threads.infra.credential_file import FileCredentialReader
from mai.modules.threads.infra.in_memory_claim_store import InMemoryClaimStore
from mai.modules.threads.jobs.session_stale_job import SessionStaleJob
from mai.platform.mailserver import parse_headers_bytes, ref_of
from mai.platform.mailserver.mail_client_factory import MailClientFactory

from .fakes import ROOT, FakeMail, RecordingReturner, State, queue

pytestmark = pytest.mark.anyio


@pytest.fixture
def anyio_backend():
    return "asyncio"


class FakeFactory:
    def __init__(self):
        self.sent = []

    def open(self, login, secret):
        factory = self

        class Session:
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

            def send(self, raw, mail_from, rcpt):
                factory.sent.append((raw, mail_from, rcpt))
                return {}

        return Session()


def as_factory(fake: FakeFactory) -> MailClientFactory:
    # The fake gives fake sessions, not MailSession objects; the job only sends.
    return cast(MailClientFactory, fake)


async def test_signal_is_sent_once_from_board(tmp_path):
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    await ClaimService(store, RecordingReturner()).claim(
        State("cursor.lane-a", FakeMail(queue())), ref=ref_of(ROOT)
    )
    cred = tmp_path / "board.cred"
    cred.write_text("mai1:board:" + "a" * 26 + "\n")
    factory = FakeFactory()
    returner = RecordingReturner()
    job = SessionStaleJob(store, as_factory(factory), FileCredentialReader(), returner)
    job.credential_file = str(cred)
    job.supervisor = "owner"
    assert await job.run_once() == []
    clock[0] += dt.timedelta(minutes=31)
    assert len(await job.run_once()) == 1
    assert await job.run_once() == []
    [(raw, mail_from, rcpt)] = factory.sent
    headers = parse_headers_bytes(raw)
    assert mail_from.startswith("board@")
    assert rcpt == ["owner@mai.internal"] or rcpt[0].startswith("owner@")
    assert headers.get("x-mai-kind") == "signal"


async def test_the_unread_mail_of_an_ended_claim_goes_back_before_the_signal(tmp_path):
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    await ClaimService(store, RecordingReturner()).claim(
        State("cursor.lane-a", FakeMail(queue())), ref=ref_of(ROOT)
    )
    cred = tmp_path / "board.cred"
    cred.write_text("mai1:board:" + "a" * 26 + "\n")
    factory = FakeFactory()
    returner = RecordingReturner(ok=False)
    job = SessionStaleJob(store, as_factory(factory), FileCredentialReader(), returner)
    job.credential_file = str(cred)
    clock[0] += dt.timedelta(minutes=31)
    # The return did not run: no signal, and the next run tries again.
    assert await job.run_once() == []
    assert factory.sent == []
    returner.ok = True
    assert len(await job.run_once()) == 1
    assert returner.calls == [("cursor.lane-a", "cursor", ROOT)] * 2
    assert len(factory.sent) == 1


async def test_a_revoked_session_gets_its_signal_also_when_the_return_fails(tmp_path):
    clock = [dt.datetime(2026, 11, 12, 9, 0, tzinfo=dt.UTC)]
    store = InMemoryClaimStore(now=lambda: clock[0])
    await ClaimService(store, RecordingReturner()).claim(
        State("cursor.lane-a", FakeMail(queue())), ref=ref_of(ROOT)
    )
    cred = tmp_path / "board.cred"
    cred.write_text("mai1:board:" + "a" * 26 + "\n")
    factory = FakeFactory()
    job = SessionStaleJob(
        store, as_factory(factory), FileCredentialReader(), RecordingReturner(ok=False)
    )
    job.credential_file = str(cred)
    clock[0] += dt.timedelta(minutes=31)
    store.dead_sessions.add("cursor.lane-a")
    assert len(await job.run_once()) == 1
    assert await job.run_once() == []
    assert len(factory.sent) == 1
