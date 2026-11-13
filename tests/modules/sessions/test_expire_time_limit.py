"""The time limit of a lease request when expired leases must end.

A lease request ends the expired leases first. Each end logs out the handle
of the lease. When the mail server does not reply, the close of the mail
connections of each handle takes its limit. The logouts occur at the same
time, thus the lease request stays in its time limit.
"""

import datetime as dt
import threading
import time

import anyio

from mai.modules.sessions.application import lease_service, resolve_session_query
from mai.modules.sessions.application.lease_service import LeaseError
from tests.modules.sessions.test_lease_service import make_parts

# The scale of the test. The server values are 20 s, 3 s, and 2 s.
REQUEST = 0.5
LOGOUT = 0.3
CLOSE = 0.2
EXPIRED = 8


class HungMail:
    """A mail session whose close waits, like an IMAP LOGOUT to a stopped Dovecot."""

    def __init__(self, login, release: threading.Event):
        self.login = login
        self._release = release

    def connect(self):
        return self

    def close(self):
        self._release.wait(10)


class HungFactory:
    def __init__(self):
        self.release = threading.Event()

    def open(self, login, secret):
        return HungMail(login, self.release)


def test_a_lease_request_with_expired_leases_stops_at_its_time_limit(
    tmp_path, monkeypatch
):
    parts = make_parts(tmp_path)
    factory = HungFactory()
    monkeypatch.setattr(parts.log_in, "_mail", factory)
    monkeypatch.setattr(resolve_session_query, "CLOSE_LIMIT_SECONDS", CLOSE)
    monkeypatch.setattr(lease_service, "LOGOUT_LIMIT_SECONDS", LOGOUT)
    start = dt.datetime.now(dt.UTC)
    parts.leases.now = lambda: start
    for pid in range(EXPIRED):
        anyio.run(parts.leases.acquire, parts.key, "claude", "/w", pid)
    # The apps stopped with no end of their leases: each lease expires.
    parts.leases.now = lambda: start + dt.timedelta(seconds=600)
    parts.leases.request_seconds = REQUEST
    began = time.monotonic()
    try:
        result = anyio.run(parts.leases.acquire, parts.key, "claude", "/w", 99)
    except LeaseError as exc:
        result = (exc.status, exc.code)
    elapsed = time.monotonic() - began
    factory.release.set()
    # The limit of the request, and one shielded logout after the limit.
    assert elapsed <= REQUEST + LOGOUT + 0.1, (round(elapsed, 2), result)
