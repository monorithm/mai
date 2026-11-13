"""The time limits of the lease operations.

Each lease operation has a time limit that is less than the request limit of
the HTTP server and less than the wait of the bridge. When the limit occurs,
the error has a code that tells the part of the server that ran at that time.
"""

import threading

import anyio
import pytest

from mai.modules.sessions.application.lease_service import (
    COUNT_LIMIT_SECONDS,
    HEARTBEAT_LIMIT_SECONDS,
    REQUEST_LIMIT_SECONDS,
    LeaseError,
)
from mai.modules.sessions.application.resolve_session_query import ResolveSessionQuery
from mai.platform.contract import ToolError
from tests.modules.sessions.test_lease_service import Parts, make_parts

# Longer than each time limit of the tests. The slow part never replies in time.
NO_REPLY_SECONDS = 60
LIMIT = 0.3


class SlowConnect:
    """A mail connect that waits until the test ends, as a mail server that does not reply."""

    def __init__(self, parts: Parts, monkeypatch) -> None:
        self.release = threading.Event()
        factory_open = parts.factory.open

        def slow_open(login, secret):
            mail = factory_open(login, secret)
            release = self.release

            def connect():
                release.wait(NO_REPLY_SECONDS)
                return mail

            mail.connect = connect
            return mail

        monkeypatch.setattr(parts.factory, "open", slow_open)
        self.undo = lambda: monkeypatch.setattr(parts.factory, "open", factory_open)

    def stop(self) -> None:
        self.release.set()
        self.undo()


@pytest.fixture
def stop_slow_connects():
    made: list[SlowConnect] = []
    yield made
    for slow in made:
        slow.release.set()


def short_limits(parts: Parts) -> Parts:
    parts.leases.request_seconds = LIMIT
    parts.leases.heartbeat_seconds = LIMIT
    parts.leases.count_seconds = LIMIT / 3
    return parts


def lease_error(fn, *args) -> LeaseError:
    with pytest.raises(LeaseError) as info:
        anyio.run(fn, *args)
    return info.value


def test_the_limits_are_less_than_the_request_limit_and_the_waits_of_the_bridge():
    # The HTTP server stops a request at 30 s. The bridge waits 30 s for a
    # lease request and 15 s for a heartbeat.
    assert REQUEST_LIMIT_SECONDS < 30
    assert COUNT_LIMIT_SECONDS < HEARTBEAT_LIMIT_SECONDS < 15


def test_a_lease_request_that_waits_for_a_different_lease_operation_gives_busy(
    tmp_path,
):
    parts = short_limits(make_parts(tmp_path))

    async def hold_the_lock(held: anyio.Event) -> None:
        async with parts.leases._lock:
            held.set()
            await anyio.sleep(NO_REPLY_SECONDS)

    async def wait_for_the_lock() -> LeaseError:
        # A different lease operation holds the lock.
        async with anyio.create_task_group() as group:
            held = anyio.Event()
            group.start_soon(hold_the_lock, held)
            await held.wait()
            try:
                await parts.leases.acquire(parts.key, "claude", "/w", 1)
            except LeaseError as exc:
                return exc
            finally:
                group.cancel_scope.cancel()
        pytest.fail("the lease request did not wait for the lock")

    error = anyio.run(wait_for_the_lock)
    assert (error.code, error.status, error.retry_safe) == ("BUSY", 503, True)


def test_a_slow_mail_login_gives_mail_server_down_and_keeps_no_lease(
    tmp_path, monkeypatch, stop_slow_connects
):
    parts = short_limits(make_parts(tmp_path))
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    stop_slow_connects.append(SlowConnect(parts, monkeypatch))
    for name in ("lane-a", None):
        error = lease_error(parts.leases.acquire, parts.key, "claude", "/w", 1, name)
        assert (error.code, error.status, error.retry_safe) == (
            "MAIL_SERVER_DOWN",
            503,
            True,
        )
    assert anyio.run(parts.leases.leases) == []


def test_a_time_limit_after_the_mail_login_ends_the_mail_login(tmp_path, monkeypatch):
    parts = short_limits(make_parts(tmp_path))
    log_in = parts.log_in.execute
    handles: list[str] = []

    async def record_log_in(command):
        state = await log_in(command)
        handles.append(state.handle)
        return state

    async def slow_put(lease):
        await anyio.sleep(NO_REPLY_SECONDS)

    monkeypatch.setattr(parts.log_in, "execute", record_log_in)
    monkeypatch.setattr(parts.lease_store, "put", slow_put)
    error = lease_error(parts.leases.acquire, parts.key, "claude", "/w", 1)
    assert (error.code, error.status) == ("STORE_UNAVAILABLE", 503)
    assert len(handles) == 1
    with pytest.raises(ToolError) as info:
        anyio.run(parts.resolve.execute, ResolveSessionQuery(handles[0]))
    assert info.value.code == "SESSION_UNKNOWN"


def test_a_slow_count_of_new_mail_gives_new_mail_null(tmp_path, monkeypatch):
    parts = short_limits(make_parts(tmp_path))
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)

    async def slow_count(state):
        await anyio.sleep(NO_REPLY_SECONDS)

    monkeypatch.setattr(parts.counter, "count", slow_count)
    beat = anyio.run(parts.leases.heartbeat, lease["lease"])
    assert beat == {"session": "cursor.s1", "new_mail": None}


def test_a_slow_store_during_a_heartbeat_gives_store_unavailable_and_keeps_the_lease(
    tmp_path, monkeypatch
):
    parts = short_limits(make_parts(tmp_path))
    lease = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 1)
    resolve = parts.resolve.execute

    async def slow_resolve(query):
        await anyio.sleep(NO_REPLY_SECONDS)

    monkeypatch.setattr(parts.resolve, "execute", slow_resolve)
    error = lease_error(parts.leases.heartbeat, lease["lease"])
    assert (error.code, error.status, error.retry_safe) == (
        "STORE_UNAVAILABLE",
        503,
        True,
    )
    monkeypatch.setattr(parts.resolve, "execute", resolve)
    beat = anyio.run(parts.leases.heartbeat, lease["lease"])
    assert beat == {"session": "cursor.s1", "new_mail": 3}


def test_a_slow_bind_gives_the_tool_error_of_the_part_and_the_lease_stays(
    tmp_path, monkeypatch, stop_slow_connects
):
    parts = short_limits(make_parts(tmp_path))
    pool = [
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
        for pid in range(8)
    ]
    extra = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    anyio.run(parts.leases.end, pool[0]["lease"])
    slow = SlowConnect(parts, monkeypatch)
    stop_slow_connects.append(slow)
    with pytest.raises(ToolError) as info:
        anyio.run(parts.leases.state, extra["lease"])
    assert info.value.code == "MAIL_SERVER_DOWN"
    assert info.value.structured()["error"]["retry_safe"] is True
    slow.stop()
    state = anyio.run(parts.leases.state, extra["lease"])
    assert state.login == "cursor.s1"
