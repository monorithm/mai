"""The request limit of the HTTP server.

The longest wait of wait_for_mail is MAX_WAIT (50 s). The request limit of
the HTTP server must be longer, else the server stops the wait with HTTP 504
and no code. The time limits of the lease operations are less than it.
"""

from mai import app_main
from mai.modules.mail.domain.mail_rules import MAX_WAIT
from mai.modules.sessions.application.lease_service import (
    HEARTBEAT_LIMIT_SECONDS,
    REQUEST_LIMIT_SECONDS,
)


def test_the_request_limit_is_longer_than_the_longest_wait_for_mail():
    limit = app_main.REQUEST_LIMITS.timeout_seconds
    assert limit is not None
    # The margin is for the steps of the tool call before and after the wait.
    assert limit >= MAX_WAIT + 20
    assert REQUEST_LIMIT_SECONDS < limit
    assert HEARTBEAT_LIMIT_SECONDS < limit


def test_the_application_serves_with_the_request_limits(monkeypatch):
    seen: dict = {}

    def fake_create_app(module, **kwargs):
        seen.update(kwargs)
        return "app"

    monkeypatch.setattr(app_main, "create_app", fake_create_app)
    assert app_main.create_asgi_app() == "app"
    assert seen["request_limits"] is app_main.REQUEST_LIMITS
