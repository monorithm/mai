"""The owner HTTP API of the sessions and the lease of a named session, on the memory backend."""

import re

import anyio
import pytest
from bustan import ConfigModule, Module
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import AgentKeys, CredentialService, CredentialsModule
from mai.modules.credentials.domain.credential_files import CredentialFiles
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
from mai.modules.credentials.infra.local_credential_files import (
    LocalCredentialFiles,
)
from mai.modules.sessions import SessionsModule
from mai.modules.sessions.domain.new_mail_counter import NewMailCounter
from mai.platform.mailserver import MailClientFactory
from tests.modules.sessions.test_lease_service import FixedCounter
from tests.modules.sessions.test_session_tools import FakeFactory

OWNER = "mai1:owner:" + "o" * 52
# A secret is 52 or more base32 characters. No answer of the owner API has one.
_SECRET = re.compile(r"[a-z2-7]{40,}")


class NoAcl(MailboxAcl):
    async def grant(self, agent, login, rights):
        return True

    async def remove(self, agent, login):
        return True


@Module(imports=[ConfigModule.for_root(), CredentialsModule, SessionsModule])
class Root:
    pass


@pytest.fixture
def api(tmp_path):
    app = create_test_app(
        Root,
        provider_overrides={
            CredentialFiles: LocalCredentialFiles(str(tmp_path / "secrets")),
            MailboxAcl: NoAcl(),
            MailClientFactory: FakeFactory(),
            NewMailCounter: FixedCounter(),
        },
    )
    with AsgiTestClient(app) as client:
        credentials = app.get(CredentialService)
        anyio.run(credentials.init_accounts, False)
        anyio.run(credentials.owner_set_key, OWNER)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        yield client, key


AUTH = {"authorization": f"Bearer {OWNER}"}


def owner_answer(response) -> dict:
    """The JSON of an owner answer, after a check that it holds no secret."""
    text = response.text
    assert "mai1:" not in text and not _SECRET.search(text), text
    return response.json()


def lease(client, key, **extra):
    body = {"app": "cursor", "folder": "/work", "pid": 7, **extra}
    return client.post(
        "/api/leases", json=body, headers={"authorization": f"Bearer {key}"}
    )


def test_each_route_needs_the_owner_key(api):
    client, key = api
    for method, path in (
        ("post", "/api/sessions"),
        ("get", "/api/sessions"),
        ("post", "/api/sessions/cursor.a/rotate"),
        ("post", "/api/sessions/cursor.a/revoke"),
        ("delete", "/api/sessions/cursor.a/lease"),
    ):
        for headers in ({}, {"authorization": f"Bearer {key}"}):
            response = getattr(client, method)(path, headers=headers)
            assert response.status_code == 401, (method, path)
            assert response.json()["error"]["code"] == "BAD_KEY"


def test_new_session_by_label_and_by_count(api):
    client, _key = api
    made = client.post(
        "/api/sessions", json={"agent": "cursor", "label": "lane-a"}, headers=AUTH
    )
    assert made.status_code == 201
    assert owner_answer(made) == {
        "login": "cursor.lane-a",
        "agent": "cursor",
        "label": "lane-a",
        "status": "active",
    }
    pool = client.post(
        "/api/sessions", json={"agent": "cursor", "count": 3}, headers=AUTH
    )
    assert pool.status_code == 201
    assert [s["login"] for s in owner_answer(pool)["sessions"]] == [
        "cursor.s1",
        "cursor.s2",
        "cursor.s3",
    ]
    again = client.post(
        "/api/sessions", json={"agent": "cursor", "count": 3}, headers=AUTH
    )
    assert again.status_code == 201 and again.json() == {"sessions": []}


@pytest.mark.parametrize(
    ("body", "status", "code"),
    [
        ({"agent": "cursor", "label": "lane-a"}, 409, "SESSION_EXISTS"),
        ({"agent": "cursor", "label": "Lane A"}, 400, "BAD_LABEL"),
        ({"agent": "cursor", "label": "a" * 33}, 400, "BAD_LABEL"),
        ({"agent": "nobody", "label": "lane-a"}, 404, "AGENT_UNKNOWN"),
        ({"agent": "cursor"}, 400, "BAD_REQUEST"),
        ({"agent": "cursor", "label": "x", "count": 2}, 400, "BAD_REQUEST"),
        ({"agent": "cursor", "count": 0}, 400, "BAD_REQUEST"),
        ({"label": "lane-a"}, 400, "BAD_REQUEST"),
    ],
)
def test_new_session_errors(api, body, status, code):
    client, _key = api
    client.post(
        "/api/sessions", json={"agent": "cursor", "label": "lane-a"}, headers=AUTH
    )
    response = client.post("/api/sessions", json=body, headers=AUTH)
    assert response.status_code == status
    assert owner_answer(response)["error"]["code"] == code


def test_the_lease_of_a_named_session_and_the_owner_view(api):
    client, key = api
    for label in ("lane-a", "lane-b"):
        client.post(
            "/api/sessions", json={"agent": "cursor", "label": label}, headers=AUTH
        )
    first = lease(client, key, session="lane-a")
    assert first.status_code == 201
    body = first.json()
    assert set(body) == {"lease", "session", "heartbeat_seconds", "expires_seconds"}
    assert body["session"] == "cursor.lane-a"
    busy = lease(client, key, session="cursor.lane-a")
    assert busy.status_code == 409 and busy.json()["error"]["code"] == "SESSION_BUSY"
    for value, status, code in (
        ("lane-z", 404, "SESSION_UNKNOWN"),
        ("codex.lane-a", 404, "SESSION_UNKNOWN"),
        ("Lane", 400, "BAD_LABEL"),
        (5, 400, "BAD_REQUEST"),
    ):
        response = lease(client, key, session=value)
        assert response.status_code == status, value
        assert response.json()["error"]["code"] == code
    pool = lease(client, key)
    assert pool.status_code == 201 and pool.json()["session"] == "cursor.s1"

    listed = client.get("/api/sessions", headers=AUTH)
    rows = {row["login"]: row for row in owner_answer(listed)["sessions"]}
    assert set(rows) == {"cursor.lane-a", "cursor.lane-b", "cursor.s1"}
    assert rows["cursor.lane-a"]["leased"] is True
    assert (rows["cursor.lane-a"]["app"], rows["cursor.lane-a"]["pid"]) == (
        "cursor",
        7,
    )
    assert rows["cursor.lane-b"]["leased"] is False
    assert rows["cursor.lane-b"]["folder"] is None


def test_rotate_revoke_and_end_lease(api):
    client, key = api
    for label in ("lane-a", "lane-b"):
        client.post(
            "/api/sessions", json={"agent": "cursor", "label": label}, headers=AUTH
        )
    token = lease(client, key, session="lane-a").json()["lease"]
    beat = {"authorization": f"Bearer {token}"}
    assert client.post("/api/leases/current/heartbeat", headers=beat).status_code == 200

    rotated = client.post("/api/sessions/cursor.lane-a/rotate", headers=AUTH)
    assert rotated.status_code == 200
    assert owner_answer(rotated) == {"login": "cursor.lane-a", "status": "active"}
    ended = client.post("/api/leases/current/heartbeat", headers=beat)
    assert ended.status_code == 401
    assert ended.json()["error"]["code"] == "LEASE_UNKNOWN"
    assert lease(client, key, session="lane-a").status_code == 201

    revoked = client.post("/api/sessions/cursor.lane-b/revoke", headers=AUTH)
    assert revoked.status_code == 200
    assert owner_answer(revoked) == {"login": "cursor.lane-b", "status": "revoked"}
    refused = lease(client, key, session="lane-b")
    assert refused.status_code == 409
    assert refused.json()["error"]["code"] == "SESSION_REVOKED"
    again = client.post("/api/sessions/cursor.lane-b/rotate", headers=AUTH)
    assert again.status_code == 409
    assert again.json()["error"]["code"] == "SESSION_REVOKED"
    for path in ("/api/sessions/cursor.none/rotate", "/api/sessions/none/revoke"):
        unknown = client.post(path, headers=AUTH)
        assert unknown.status_code == 404
        assert unknown.json()["error"]["code"] == "SESSION_UNKNOWN"

    end = client.delete("/api/sessions/cursor.lane-a/lease", headers=AUTH)
    assert end.status_code == 204
    gone = client.delete("/api/sessions/cursor.lane-a/lease", headers=AUTH)
    assert gone.status_code == 404
