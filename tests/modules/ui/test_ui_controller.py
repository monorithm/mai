import re

import pytest
from bustan import ClassProvider, ConfigModule, Module, ValueProvider
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.board import BoardTools
from mai.modules.credentials import CredentialService
from mai.modules.ui.api.ui_controller import UiController
from mai.modules.ui.application.board_snapshot_query import BoardSnapshotHandler
from mai.modules.ui.application.current_session_query import CurrentSessionHandler
from mai.modules.ui.application.manage_sessions_command import (
    ManageSessionsHandler,
)
from mai.modules.ui.application.sign_in_command import SignInHandler
from mai.modules.ui.application.sign_out_command import SignOutHandler
from mai.modules.ui.domain.owner_session import COOKIE, OwnerSessionStore
from mai.modules.ui.infra.memory_owner_session_store import MemoryOwnerSessionStore
from mai.platform.clock import ClockModule

OWNER = "mai1:owner:right-secret"
FORM = {"content-type": "application/x-www-form-urlencoded"}


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

    async def session_new(self, agent, label, expires_days=7):
        self.calls.append(("new", agent, label, expires_days))
        return {
            "login": f"{agent}-{label}",
            "credential": "mai1:x:shown-once",
            "expires_at": "2026-12-01",
        }

    async def session_rotate(self, login):
        self.calls.append(("rotate", login))
        return {
            "login": login,
            "credential": "mai1:y:rotated",
            "expires_at": "2026-12-01",
        }

    async def session_revoke(self, login):
        self.calls.append(("revoke", login))
        return {"login": login, "status": "revoked"}


class FakeBoard:
    def tools(self):
        async def get_board(ctx, args):
            return {"items": [{"id": "B-1", "title": "<b>first</b>"}]}

        return {"get_board": get_board}


@pytest.fixture
def credentials():
    return FakeCredentials()


@pytest.fixture
def client(credentials):
    @Module(
        imports=[ConfigModule.for_root(), ClockModule],
        controllers=[UiController],
        providers=[
            ValueProvider(provide=CredentialService, use_value=credentials),
            ValueProvider(provide=BoardTools, use_value=FakeBoard()),
            ClassProvider(provide=OwnerSessionStore, use_class=MemoryOwnerSessionStore),
            SignInHandler,
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


def _sign_in(client):
    r = client.post(
        "/ui/login",
        content=f"credential={OWNER}".encode(),
        headers=FORM,
        follow_redirects=False,
    )
    assert r.status_code == 303
    cookie = r.headers["set-cookie"]
    assert "HttpOnly" in cookie and "SameSite=Strict" in cookie and "Secure" in cookie
    token_match = re.match(rf"{COOKIE}=([^;]+)", cookie)
    assert token_match is not None
    token = token_match.group(1)
    page = client.get("/ui/sessions", headers={"cookie": f"{COOKIE}={token}"})
    csrf_match = re.search(r"name=csrf value='([^']+)'", page.text)
    assert csrf_match is not None
    csrf = csrf_match.group(1)
    return {"cookie": f"{COOKIE}={token}"}, csrf


def test_login_page_has_a_strict_csp(client):
    r = client.get("/ui/login")
    assert r.status_code == 200
    assert "default-src 'none'" in r.headers["content-security-policy"]
    assert r.headers["x-frame-options"] == "DENY"
    assert "<script" not in r.text


def test_wrong_credential_is_refused(client):
    r = client.post("/ui/login", content=b"credential=mai1:owner:wrong", headers=FORM)
    assert r.status_code == 401
    assert "set-cookie" not in r.headers


def test_pages_need_a_sign_in(client):
    r = client.get("/ui/sessions", follow_redirects=False)
    assert r.status_code == 303 and r.headers["location"] == "/ui/login"


def test_sessions_page_and_actions(client, credentials):
    cookie, csrf = _sign_in(client)
    page = client.get("/ui/sessions", headers=cookie)
    assert "cursor-s1" in page.text
    assert "<select name=agent required>" in page.text
    assert "<option value='designer'>designer</option>" in page.text
    body = f"csrf={csrf}&agent=cursor&label=s2&days=3".encode()
    r = client.post("/ui/sessions/new", content=body, headers={**cookie, **FORM})
    assert r.status_code == 200 and "shown-once" in r.text
    r = client.post(
        "/ui/sessions/rotate",
        content=f"csrf={csrf}&login=cursor-s1".encode(),
        headers={**cookie, **FORM},
    )
    assert "rotated" in r.text
    r = client.post(
        "/ui/sessions/revoke",
        content=f"csrf={csrf}&login=cursor-s1".encode(),
        headers={**cookie, **FORM},
    )
    assert "Revoked cursor-s1." in r.text
    assert credentials.calls == [
        ("new", "cursor", "s2", 3),
        ("rotate", "cursor-s1"),
        ("revoke", "cursor-s1"),
    ]


def test_the_sessions_page_tells_how_to_add_an_agent_when_there_is_none(
    client, credentials
):
    credentials.agents = []
    cookie, _ = _sign_in(client)
    page = client.get("/ui/sessions", headers=cookie)
    assert "mai agent add NAME" in page.text
    assert "<select name=agent" not in page.text


def test_post_without_the_csrf_token_is_refused(client, credentials):
    cookie, _ = _sign_in(client)
    r = client.post(
        "/ui/sessions/revoke",
        content=b"csrf=bad&login=cursor-s1",
        headers={**cookie, **FORM},
    )
    assert r.status_code == 403
    assert credentials.calls == []


def test_board_page_escapes_the_content(client):
    cookie, _ = _sign_in(client)
    r = client.get("/ui/board", headers=cookie)
    assert r.status_code == 200
    assert "B-1" in r.text and "<b>first</b>" not in r.text


def test_logout_ends_the_session(client):
    cookie, csrf = _sign_in(client)
    r = client.post(
        "/ui/logout",
        content=f"csrf={csrf}".encode(),
        headers={**cookie, **FORM},
        follow_redirects=False,
    )
    assert r.status_code == 303
    r = client.get("/ui/sessions", headers=cookie, follow_redirects=False)
    assert r.headers["location"] == "/ui/login"


def test_style_sheet_is_served(client):
    r = client.get("/ui/style.css")
    assert r.status_code == 200 and "body{" in r.text
