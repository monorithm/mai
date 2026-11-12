import re
from datetime import UTC, datetime, timedelta

import pytest
from bustan import ClassProvider, ConfigModule, Module, ValueProvider
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.board import BoardTools
from mai.modules.credentials import CredentialService
from mai.modules.ui.api.ui_api_controller import UiApiController
from mai.modules.ui.api.ui_controller import UiController
from mai.modules.ui.api.ui_http_policy import UiHttpPolicy
from mai.modules.ui.application.board_snapshot_query import BoardSnapshotHandler
from mai.modules.ui.application.current_session_query import CurrentSessionHandler
from mai.modules.ui.application.manage_sessions_command import (
    ManageSessionsHandler,
)
from mai.modules.ui.application.sign_out_command import SignOutHandler
from mai.modules.ui.application.ui_access_service import UiAccessService
from mai.modules.ui.domain.mail_view import (
    EnvelopeLookup,
    MailboxMissingError,
    MailReader,
    MailSummary,
    ThreadClaim,
    ThreadLookup,
)
from mai.modules.ui.domain.owner_session import COOKIE, OwnerSessionStore
from mai.modules.ui.domain.sign_in_ticket import SignInTicketStore
from mai.modules.ui.domain.ui_state import UiStateStore
from mai.modules.ui.infra.memory_owner_session_store import MemoryOwnerSessionStore
from mai.modules.ui.infra.memory_ui_state import (
    MemorySignInTicketStore,
    MemoryUiStateStore,
)
from mai.platform.clock import Clock

OWNER = "mai1:owner:right-secret"
BEARER = {"authorization": f"Bearer {OWNER}"}
FORM = {"content-type": "application/x-www-form-urlencoded"}

HTML_MAIL = (
    b"From: alpha@mai.internal\r\nTo: beta@mai.internal\r\n"
    b"Subject: <b>hello</b>\r\nMessage-ID: <m1@mai.internal>\r\n"
    b"MIME-Version: 1.0\r\n"
    b'Content-Type: multipart/alternative; boundary="b"\r\n\r\n'
    b"--b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"
    b"plain <script>alert(1)</script> text\r\n"
    b"--b\r\nContent-Type: text/html; charset=utf-8\r\n\r\n"
    b"<p onclick=x>html part</p>\r\n--b--\r\n"
)


class FakeClock(Clock):
    def __init__(self):
        self.at = datetime(2026, 11, 12, 12, 0, tzinfo=UTC)

    def now(self):
        return self.at


class FakeCredentials:
    def __init__(self):
        self.calls = []
        self.rows = [
            {
                "login": "cursor-s1",
                "agent": "cursor",
                "status": "active",
                "expires_at": "2026-12-01",
            }
        ]
        self.agents = ["cursor", "designer"]

    async def agent_names(self):
        return list(self.agents)

    async def authenticate_owner(self, credential):
        return {"kind": "owner"} if credential == OWNER else None

    async def session_list(self, agent=None):
        return list(self.rows)

    async def session_revoke(self, login):
        self.calls.append(("revoke", login))
        return {"login": login, "status": "revoked"}


class FakeBoard:
    def tools(self):
        async def get_board(ctx, args):
            return {"items": [{"id": "B-1", "title": "<b>first</b>"}]}

        return {"get_board": get_board}


class FakeReader(MailReader):
    def __init__(self):
        self.mailboxes = {
            "copies": {"1": HTML_MAIL},
            "cursor": {"7": b"Subject: queued\r\n\r\nfor cursor\r\n"},
        }
        self.searches = []

    async def messages(self, user, search=""):
        if user not in self.mailboxes:
            raise MailboxMissingError(user)
        self.searches.append((user, search))
        return [
            MailSummary(
                uid, "2026-11-12", "alpha", f"subject {uid}", "<m1@mai.internal>"
            )
            for uid in sorted(self.mailboxes[user], reverse=True)
        ]

    async def raw(self, user, uid):
        return self.mailboxes.get(user, {}).get(uid)


class FakeThreads(ThreadLookup):
    async def claims(self):
        return [
            ThreadClaim("cursor", "<r1@mai.internal>", "cursor.s1", "2026-11-12", True)
        ]


class FakeEnvelopes(EnvelopeLookup):
    async def find(self, message_ids):
        return {
            "<m1@mai.internal>": {"message_id": "<m1@mai.internal>", "queue_id": "Q1"}
        }


@pytest.fixture
def clock():
    return FakeClock()


@pytest.fixture
def credentials():
    return FakeCredentials()


@pytest.fixture
def reader():
    return FakeReader()


@pytest.fixture
def client(credentials, clock, reader):
    @Module(
        imports=[ConfigModule.for_root()],
        controllers=[UiApiController, UiController],
        providers=[
            ValueProvider(provide=Clock, use_value=clock),
            ValueProvider(provide=CredentialService, use_value=credentials),
            ValueProvider(provide=BoardTools, use_value=FakeBoard()),
            ValueProvider(provide=MailReader, use_value=reader),
            ValueProvider(provide=EnvelopeLookup, use_value=FakeEnvelopes()),
            ValueProvider(provide=ThreadLookup, use_value=FakeThreads()),
            ValueProvider(
                provide=UiHttpPolicy,
                use_value=UiHttpPolicy(
                    frozenset({"testserver"}), frozenset({"http://testserver"})
                ),
            ),
            ClassProvider(provide=UiStateStore, use_class=MemoryUiStateStore),
            ClassProvider(provide=SignInTicketStore, use_class=MemorySignInTicketStore),
            ClassProvider(provide=OwnerSessionStore, use_class=MemoryOwnerSessionStore),
            UiAccessService,
            SignOutHandler,
            CurrentSessionHandler,
            ManageSessionsHandler,
            BoardSnapshotHandler,
        ],
    )
    class Root:
        pass

    with AsgiTestClient(create_test_app(Root)) as c:
        yield c


def _turn_on(client):
    r = client.put("/api/ui", json={"enabled": True}, headers=BEARER)
    assert r.status_code == 200 and r.json() == {"enabled": True}


def _ticket(client):
    r = client.post("/api/ui/tickets", headers=BEARER)
    assert r.status_code == 201
    assert r.json()["expires_seconds"] == 60
    return r.json()["ticket"]


def _sign_in(client):
    _turn_on(client)
    r = client.post("/ui/session", json={"ticket": _ticket(client)})
    assert r.status_code == 204
    cookie = r.headers["set-cookie"]
    match = re.match(rf"{COOKIE}=([^;]+)", cookie)
    assert match is not None
    headers = {"cookie": f"{COOKIE}={match.group(1)}"}
    page = client.get("/ui/sessions", headers=headers)
    csrf = re.search(r"name=csrf value='([^']+)'", page.text)
    assert csrf is not None
    return headers, csrf.group(1)


def test_the_ui_is_off_by_default(client):
    r = client.get("/api/ui", headers=BEARER)
    assert r.status_code == 200 and r.json() == {"enabled": False}


def test_the_state_needs_the_owner_key(client):
    assert client.get("/api/ui").status_code == 401
    r = client.put(
        "/api/ui", json={"enabled": True}, headers={"authorization": "Bearer bad"}
    )
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "BAD_KEY"


def test_the_state_refuses_a_bad_body(client):
    r = client.put("/api/ui", json={"enabled": "yes"}, headers=BEARER)
    assert r.status_code == 400


def test_each_ui_route_gives_404_when_off(client):
    for path in (
        "/ui/",
        "/ui/sign-in",
        "/ui/sign-in.js",
        "/ui/style.css",
        "/ui/mail",
        "/ui/inbox",
        "/ui/agents",
        "/ui/sessions",
        "/ui/board",
        "/ui/threads",
    ):
        assert client.get(path).status_code == 404, path
    assert client.post("/ui/session", json={"ticket": "x"}).status_code == 404


def test_a_ticket_needs_the_ui_on(client):
    r = client.post("/api/ui/tickets", headers=BEARER)
    assert r.status_code == 409
    assert r.json()["error"]["code"] == "UI_OFF"


def test_a_ticket_is_valid_one_time(client):
    _turn_on(client)
    ticket = _ticket(client)
    assert client.post("/ui/session", json={"ticket": ticket}).status_code == 204
    r = client.post("/ui/session", json={"ticket": ticket})
    assert r.status_code == 401 and "set-cookie" not in r.headers


def test_a_ticket_expires_after_60_seconds(client, clock):
    _turn_on(client)
    ticket = _ticket(client)
    clock.at += timedelta(seconds=61)
    assert client.post("/ui/session", json={"ticket": ticket}).status_code == 401


def test_the_cookie_has_the_strict_flags(client):
    _turn_on(client)
    r = client.post("/ui/session", json={"ticket": _ticket(client)})
    cookie = r.headers["set-cookie"]
    assert "HttpOnly" in cookie
    assert "SameSite=Strict" in cookie
    assert "Path=/ui;" in cookie
    assert f"Max-Age={12 * 3600}" in cookie


def test_the_session_ends_after_the_idle_limit(client, clock):
    headers, _ = _sign_in(client)
    clock.at += timedelta(minutes=29)
    assert client.get("/ui/mail", headers=headers).status_code == 200
    clock.at += timedelta(minutes=31)
    assert client.get("/ui/mail", headers=headers).status_code == 401


def test_the_session_ends_after_12_hours(client, clock):
    headers, _ = _sign_in(client)
    for _ in range(25):
        clock.at += timedelta(minutes=29)
        client.get("/ui/mail", headers=headers)
    assert client.get("/ui/mail", headers=headers).status_code == 401


def test_turning_the_ui_off_ends_the_sessions(client):
    headers, _ = _sign_in(client)
    client.put("/api/ui", json={"enabled": False}, headers=BEARER)
    _turn_on(client)
    assert client.get("/ui/mail", headers=headers).status_code == 401


def test_the_sign_in_page_has_a_script_from_self_only(client):
    _turn_on(client)
    r = client.get("/ui/sign-in")
    assert r.status_code == 200
    assert "script-src 'self'" in r.headers["content-security-policy"]
    assert "<script src='/ui/sign-in.js'></script>" in r.text
    js = client.get("/ui/sign-in.js")
    assert "history.replaceState" in js.text and "/ui/session" in js.text


def test_the_2x_credential_form_is_gone(client):
    _turn_on(client)
    r = client.post("/ui/login", content=f"credential={OWNER}".encode(), headers=FORM)
    assert r.status_code in (404, 405)
    assert "set-cookie" not in r.headers


def test_pages_need_a_sign_in(client):
    _turn_on(client)
    r = client.get("/ui/mail", follow_redirects=False)
    assert r.status_code == 401 and "mai ui open" in r.text


def test_all_mail_lists_the_copies_with_the_envelope(client, reader):
    headers, _ = _sign_in(client)
    r = client.get("/ui/mail?q=hello", headers=headers)
    assert r.status_code == 200
    assert "subject 1" in r.text and "queue_id: Q1" in r.text
    assert reader.searches[-1] == ("copies", "hello")


def test_all_mail_says_when_the_copies_mailbox_is_missing(client, reader):
    del reader.mailboxes["copies"]
    headers, _ = _sign_in(client)
    r = client.get("/ui/mail", headers=headers)
    assert r.status_code == 200 and "does not exist" in r.text


def test_a_mail_shows_the_text_part_escaped_and_never_the_html(client):
    headers, _ = _sign_in(client)
    r = client.get("/ui/mail/1", headers=headers)
    assert r.status_code == 200
    assert "&lt;script&gt;alert(1)&lt;/script&gt;" in r.text
    assert "<script>" not in r.text
    assert "html part" not in r.text
    assert "&lt;b&gt;hello&lt;/b&gt;" in r.text


def test_a_missing_mail_gives_404(client):
    headers, _ = _sign_in(client)
    assert client.get("/ui/mail/99", headers=headers).status_code == 404


def test_the_agent_queue_is_read_only_and_needs_a_known_agent(client):
    headers, _ = _sign_in(client)
    r = client.get("/ui/agents", headers=headers)
    assert "/ui/agents/cursor" in r.text
    r = client.get("/ui/agents/cursor", headers=headers)
    assert r.status_code == 200 and "subject 7" in r.text
    assert client.get("/ui/agents/cursor/7", headers=headers).status_code == 200
    assert client.get("/ui/agents/-u", headers=headers).status_code == 404
    assert client.get("/ui/agents/nobody", headers=headers).status_code == 404


def test_the_sessions_page_shows_no_secret_action(client, credentials):
    headers, csrf = _sign_in(client)
    page = client.get("/ui/sessions", headers=headers)
    assert "cursor-s1" in page.text
    assert "/ui/sessions/new" not in page.text
    assert "/ui/sessions/rotate" not in page.text
    r = client.post(
        "/ui/sessions/revoke",
        content=f"csrf={csrf}&login=cursor-s1".encode(),
        headers={**headers, **FORM},
    )
    assert "Revoked cursor-s1." in r.text
    assert credentials.calls == [("revoke", "cursor-s1")]


def test_post_without_the_csrf_token_is_refused(client, credentials):
    headers, _ = _sign_in(client)
    r = client.post(
        "/ui/sessions/revoke",
        content=b"csrf=bad&login=cursor-s1",
        headers={**headers, **FORM},
    )
    assert r.status_code == 403
    assert credentials.calls == []


def test_board_page_escapes_the_content(client):
    headers, _ = _sign_in(client)
    r = client.get("/ui/board", headers=headers)
    assert r.status_code == 200
    assert "B-1" in r.text and "<b>first</b>" not in r.text


def test_logout_ends_the_session(client):
    headers, csrf = _sign_in(client)
    r = client.post(
        "/ui/logout",
        content=f"csrf={csrf}".encode(),
        headers={**headers, **FORM},
        follow_redirects=False,
    )
    assert r.status_code == 303
    assert client.get("/ui/mail", headers=headers).status_code == 401


def test_style_sheet_is_served_when_on(client):
    _turn_on(client)
    r = client.get("/ui/style.css")
    assert r.status_code == 200 and "body{" in r.text


def test_the_threads_page_lists_the_claims_escaped(client):
    headers, _ = _sign_in(client)
    r = client.get("/ui/threads", headers=headers)
    assert r.status_code == 200
    assert "&lt;r1@mai.internal&gt;" in r.text and "claimed" in r.text


def test_a_host_or_an_origin_out_of_the_rules_is_refused(client):
    headers, _ = _sign_in(client)
    r = client.get("/ui/mail", headers={**headers, "host": "evil.example:8765"})
    assert r.status_code == 403
    r = client.get("/ui/mail", headers={**headers, "origin": "http://evil.example"})
    assert r.status_code == 403
    assert client.get("/ui/mail", headers=headers).status_code == 200


def test_the_policy_reads_the_hosts_of_the_environment(monkeypatch):
    monkeypatch.setenv("MAI_HOSTS", "127.0.0.1:18780")
    policy = UiHttpPolicy.from_environment()
    assert policy.admits("127.0.0.1:18780", None)
    assert policy.admits("127.0.0.1:8765", "http://127.0.0.1:8765")
    assert not policy.admits("evil.example", None)
