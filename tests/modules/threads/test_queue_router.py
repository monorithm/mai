import asyncio
import threading
from typing import cast

import pytest

from mai.modules.credentials import CredentialService
from mai.modules.threads.application.queue_router import (
    CREDENTIAL_WAIT,
    MAX_BACKOFF,
    RECHECK_AFTER,
    CredentialNotReady,
    QueueRouter,
    QueueWorker,
    next_backoff,
)
from mai.modules.threads.domain.claim import ClaimStore
from mai.modules.threads.domain.credentials import CredentialReader
from mai.modules.threads.domain.mover import QueueMover
from mai.modules.threads.infra.doveadm_mover import DoveadmMover
from mai.platform.mailserver.mail_client_factory import MailClientFactory

from .fakes import ROOT, FakeMail, headers, queue


class FakeAccounts:
    """The agent list of the credential store. A test changes names, or sets fail."""

    def __init__(self, names: tuple[str, ...] | list[str] = ()):
        self.names: list[str] = list(names)
        self.fail = False

    async def agent_names(self) -> list[str]:
        if self.fail:
            raise OSError("the store is down")
        return sorted(self.names)


def unwired_router(
    store: ClaimStore | None = None, mover=None, accounts=None
) -> QueueRouter:
    """A router without the ports that a test does not use.

    The casts state that the test never calls the absent ports.
    """
    return QueueRouter(
        cast(ClaimStore, store),
        cast(MailClientFactory, None),
        cast(QueueMover, mover),
        cast(CredentialReader, None),
        cast(CredentialService, accounts or FakeAccounts()),
    )


def counting_wait(stop: threading.Event, waits: list, limit: int):
    """A wait that records each timeout and sets stop at the given count. It gives False."""

    def wait(timeout: float | None = None) -> bool:
        waits.append(timeout)
        if len(waits) >= limit:
            stop.set()
        return False

    return wait


class RecordingMover(QueueMover):
    """A mover that calls the given function and gives True."""

    def __init__(self, on_move):
        self.on_move = on_move

    def __call__(self, session_login: str, agent: str, uid: int) -> bool:
        self.on_move(session_login, agent, uid)
        return True


def test_worker_moves_reply_of_live_claim():
    mail = FakeMail(queue())

    def route(agent, root, uid):
        if root != ROOT:
            return None
        mail.boxes["shared/cursor/INBOX"].pop(uid)
        return "cursor.lane-a"

    w = QueueWorker("cursor", lambda: mail, route, threading.Event())
    w.mail = mail
    assert w.scan() == [("cursor", "cursor.lane-a", 2)]
    assert w.scan() == []  # a mail is looked at once


def test_worker_keeps_one_login_and_idles_on_one_mailbox():
    stop = threading.Event()
    # The fake IDLE returns at once, thus the worker waits before the next IDLE: no real delay here.
    stop.wait = lambda timeout=None: stop.is_set()
    opened = []

    class Mail(FakeMail):
        def idle(self, mailboxes, timeout):
            assert mailboxes == ["shared/cursor/INBOX"]
            self.idle_calls += 1
            if self.idle_calls >= 5:
                stop.set()
            return False

    def open_mail():
        m = Mail(queue())
        opened.append(m)
        return m

    w = QueueWorker("cursor", open_mail, lambda a, r, u: None, stop)
    w.run()
    assert len(opened) == 1 and opened[0].idle_calls == 5


def test_worker_reconnects_with_backoff():
    stop = threading.Event()
    waits = []
    stop.wait = counting_wait(stop, waits, 4)
    w = QueueWorker(
        "cursor",
        lambda: (_ for _ in ()).throw(OSError("down")),
        lambda a, r, u: None,
        stop,
    )
    w.run()
    assert waits == [1.0, 2.0, 4.0, 8.0]
    assert next_backoff(MAX_BACKOFF) == MAX_BACKOFF


def test_doveadm_move_command(monkeypatch):
    calls = []

    class R:
        returncode = 0

    mover = DoveadmMover()
    monkeypatch.setattr(mover, "run", lambda cmd, **kw: calls.append(cmd) or R())
    assert mover("cursor.lane-a", "cursor", 7) is True
    assert calls[0] == [
        "doveadm",
        "move",
        "-u",
        "cursor.lane-a",
        "INBOX",
        "mailbox",
        "shared/cursor/INBOX",
        "uid",
        "7",
    ]


def test_doveadm_move_command_goes_to_the_doveadm_server_socket(tmp_path, monkeypatch):
    # The app user cannot run a local doveadm mail command, thus the move uses the server socket.
    sock = tmp_path / "doveadm-server"
    sock.touch()
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", str(sock))
    assert DoveadmMover().command("cursor.lane-a", "cursor", 7) == [
        "doveadm",
        "move",
        "-S",
        str(sock),
        "-u",
        "cursor.lane-a",
        "INBOX",
        "mailbox",
        "shared/cursor/INBOX",
        "uid",
        "7",
    ]


def test_worker_waits_without_a_warning_until_the_board_credential_exists(caplog):
    stop = threading.Event()
    waits = []
    stop.wait = counting_wait(stop, waits, 3)

    def not_ready():
        raise CredentialNotReady("/data/secrets/board.cred")

    w = QueueWorker("cursor", not_ready, lambda a, r, u: None, stop)
    with caplog.at_level("WARNING", logger="mai.router"):
        w.run()
    assert waits == [CREDENTIAL_WAIT] * 3
    assert caplog.records == []


def test_router_gives_not_ready_when_the_board_credential_file_is_missing(
    tmp_path, monkeypatch
):
    monkeypatch.setenv("MAI_BOARD_CRED", str(tmp_path / "board.cred"))
    router = unwired_router()
    with pytest.raises(CredentialNotReady):
        router._open_mail()


def test_router_runs_only_in_the_process_of_the_http_server(monkeypatch):
    monkeypatch.delenv("MAI_BACKGROUND", raising=False)
    assert unwired_router().enabled is False
    monkeypatch.setenv("MAI_BACKGROUND", "on")
    assert unwired_router().enabled is True
    monkeypatch.setenv("THREADS_ROUTER", "off")
    assert unwired_router().enabled is False


def test_worker_does_not_spin_when_idle_returns_at_once():
    stop = threading.Event()
    waits = []
    stop.wait = counting_wait(stop, waits, 3)

    class NoAccessMail:
        def search(self, mailbox, criteria):
            return []

        def idle(self, mailboxes, timeout):
            return False

        def close(self):
            pass

    w = QueueWorker("cursor", NoAccessMail, lambda a, r, u: None, stop)
    w.run()
    assert waits[:3] == [2.0, 2.0, 2.0]


def test_a_mail_with_an_error_at_its_look_is_examined_again_at_the_next_scan():
    mail = FakeMail(queue())
    calls = []

    def route(agent, root, uid):
        calls.append(uid)
        if len(calls) == 1:
            raise TimeoutError("the app loop did not answer")
        mail.boxes["shared/cursor/INBOX"].pop(uid)
        return "cursor.lane-a"

    w = QueueWorker("cursor", lambda: mail, route, threading.Event())
    w.mail = mail
    with pytest.raises(TimeoutError):
        w.scan()
    assert w.scan() == [("cursor", "cursor.lane-a", 2)]
    assert calls == [2, 2]


def test_a_move_that_leaves_the_mail_in_the_queue_is_not_a_move_and_is_tried_again(
    caplog,
):
    mail = FakeMail(queue())
    calls = []

    def route(agent, root, uid):
        calls.append(uid)
        return "cursor.lane-a"  # doveadm gave 0, but the mail did not move

    w = QueueWorker("cursor", lambda: mail, route, threading.Event())
    w.mail = mail
    with caplog.at_level("WARNING", logger="mai.router"):
        assert w.scan() == []
    assert any("left the mail in the queue" in r.getMessage() for r in caplog.records)
    assert w.scan() == []  # not before RECHECK_AFTER
    w.seen[2] -= RECHECK_AFTER
    w.scan()
    assert calls == [2, 2]


def _router(store, moved):
    def mover(login, agent, uid):
        moved.append((login, agent, uid))

    return unwired_router(store, RecordingMover(mover))


def test_route_moves_only_for_a_live_claim_of_a_live_session():
    import datetime as dt

    import anyio

    from mai.modules.threads.infra.in_memory_claim_store import InMemoryClaimStore

    store = InMemoryClaimStore()
    moved = []
    router = _router(store, moved)

    async def go():
        assert await router._route_locked("cursor", ROOT, 2) is None
        async with store.locked("cursor", ROOT) as txn:
            await txn.put("cursor.lane-a", dt.timedelta(minutes=30))
        assert await router._route_locked("cursor", ROOT, 2) == "cursor.lane-a"
        store.dead_sessions.add("cursor.lane-a")
        assert await router._route_locked("cursor", ROOT, 3) is None

    anyio.run(go)
    assert moved == [("cursor.lane-a", "cursor", 2)]


def test_route_holds_the_claim_lock_during_the_move():
    import datetime as dt

    import anyio

    from mai.modules.threads.infra.in_memory_claim_store import InMemoryClaimStore

    store = InMemoryClaimStore()
    seen_locked = []

    def mover(login, agent, uid):
        seen_locked.append(store._locks[("cursor", ROOT)].locked())

    router = unwired_router(store, RecordingMover(mover))

    async def go():
        async with store.locked("cursor", ROOT) as txn:
            await txn.put("cursor.lane-a", dt.timedelta(minutes=30))
        await router._route_locked("cursor", ROOT, 2)

    anyio.run(go)
    assert seen_locked == [True]


def test_the_return_query_puts_the_or_in_parentheses():
    from mai.modules.threads.infra.doveadm_returner import DoveadmReturner

    argv = DoveadmReturner().command("cursor.lane-a", "cursor", ROOT)
    query = argv[argv.index("INBOX", argv.index("mailbox")) + 1 :]
    assert query == [
        "unseen",
        "(",
        "header",
        "References",
        ROOT,
        "OR",
        "header",
        "In-Reply-To",
        ROOT,
        "OR",
        "header",
        "Message-ID",
        ROOT,
        ")",
    ]


def test_one_bad_mail_does_not_stop_the_other_mails_of_the_queue(caplog):
    boxes = queue()
    boxes["shared/cursor/INBOX"][4] = (
        headers("<msg-r2@mai.internal>", refs=ROOT),
        set(),
    )
    mail = FakeMail(boxes)
    calls = []

    def route(agent, root, uid):
        calls.append(uid)
        if uid == 2:
            raise ValueError("a header that the parser does not accept")
        mail.boxes["shared/cursor/INBOX"].pop(uid)
        return "cursor.lane-a"

    w = QueueWorker("cursor", lambda: mail, route, threading.Event())
    w.mail = mail
    with caplog.at_level("WARNING", logger="mai.router"):
        assert w.scan() == [("cursor", "cursor.lane-a", 4)]
    assert calls == [2, 4]
    assert w.scan() == []  # the bad mail waits for RECHECK_AFTER
    w.seen[2] -= RECHECK_AFTER
    with caplog.at_level("WARNING", logger="mai.router"):
        w.scan()
    assert calls == [2, 4, 2]


def test_a_route_that_times_out_is_cancelled(monkeypatch):
    import concurrent.futures

    from mai.modules.threads.application import queue_router as qr

    cancelled = []

    class Future:
        def result(self, timeout):
            raise concurrent.futures.TimeoutError

        def cancel(self):
            cancelled.append(True)

    def fake_submit(coro, loop):
        coro.close()
        return Future()

    monkeypatch.setattr(asyncio, "run_coroutine_threadsafe", fake_submit)
    router = unwired_router()
    # The fake submit does not use the loop; any object stands for it.
    router._loop = cast(asyncio.AbstractEventLoop, object())
    with pytest.raises(concurrent.futures.TimeoutError):
        router._route("cursor", ROOT, 2)
    assert cancelled == [True]
    assert qr._is_connection_error(concurrent.futures.TimeoutError())


def test_the_router_starts_a_worker_for_each_agent_of_the_store_and_for_each_new_one(
    monkeypatch, caplog
):
    started = []
    monkeypatch.setenv("MAI_BACKGROUND", "on")
    monkeypatch.delenv("THREADS_ROUTER", raising=False)
    accounts = FakeAccounts(["writer", "architect"])
    router = unwired_router(accounts=accounts)
    monkeypatch.setattr(
        router, "_start_worker", lambda a: (started.append(a), router.agents.append(a))
    )

    async def go():
        await router.on_application_bootstrap()
        assert started == ["architect", "writer"]
        accounts.names.append("reviewer")
        assert await router.refresh() == ["reviewer"]
        assert await router.refresh() == []
        accounts.fail = True
        with caplog.at_level("WARNING", logger="mai.router"):
            assert await router.refresh() == []
        await router.on_application_shutdown()

    asyncio.run(go())
    assert started == ["architect", "writer", "reviewer"]
    assert any("agent list" in r.getMessage() for r in caplog.records)
    assert router.agents == []


def test_the_router_reads_the_agent_list_again_after_the_refresh_time(monkeypatch):
    monkeypatch.setenv("MAI_BACKGROUND", "on")
    monkeypatch.delenv("THREADS_ROUTER", raising=False)
    accounts = FakeAccounts()
    router = unwired_router(accounts=accounts)
    router.refresh_every = 0.01
    monkeypatch.setattr(router, "_start_worker", router.agents.append)

    async def go():
        await router.on_application_bootstrap()
        assert router.agents == []
        accounts.names.append("designer")
        for _ in range(100):
            if router.agents:
                break
            await asyncio.sleep(0.01)
        found = list(router.agents)
        await router.on_application_shutdown()
        return found

    assert asyncio.run(go()) == ["designer"]


@pytest.mark.parametrize(
    ("value", "seconds"), [(None, 30.0), ("2", 2.0), ("0", 30.0), ("x", 30.0)]
)
def test_the_refresh_time_of_the_agent_list_comes_from_the_environment(
    monkeypatch, value, seconds
):
    if value is None:
        monkeypatch.delenv("THREADS_AGENT_REFRESH", raising=False)
    else:
        monkeypatch.setenv("THREADS_AGENT_REFRESH", value)
    assert unwired_router().refresh_every == seconds
