"""The lease API gives an error code for each failure of a lease request."""

import anyio
import pytest
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import AgentKeys, CredentialService
from mai.modules.credentials.domain.credential_files import CredentialFiles
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
from mai.modules.credentials.infra.local_credential_files import (
    LocalCredentialFiles,
)
from mai.modules.sessions.domain.new_mail_counter import NewMailCounter
from mai.platform.mailserver import MailClientFactory
from tests.modules.sessions.test_lease_service import FixedCounter
from tests.modules.sessions.test_session_tools import FakeFactory
from tests.modules.sessions.test_sessions_api import AUTH, OWNER, NoAcl, Root


class DownMail:
    """A mail session of a mail server that does not reply."""

    def connect(self):
        raise OSError("connection refused")

    def close(self):
        pass


class DownFactory:
    def open(self, login, secret):
        return DownMail()


@pytest.fixture
def down_api(tmp_path):
    app = create_test_app(
        Root,
        provider_overrides={
            CredentialFiles: LocalCredentialFiles(str(tmp_path / "secrets")),
            MailboxAcl: NoAcl(),
            MailClientFactory: DownFactory(),
            NewMailCounter: FixedCounter(),
        },
    )
    with AsgiTestClient(app) as client:
        credentials = app.get(CredentialService)
        anyio.run(credentials.init_accounts, False)
        anyio.run(credentials.owner_set_key, OWNER)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        yield client, key


def test_a_lease_request_when_the_mail_server_is_down_gives_mail_server_down(
    down_api,
):
    client, key = down_api
    made = client.post(
        "/api/sessions", json={"agent": "cursor", "label": "lane-a"}, headers=AUTH
    )
    assert made.status_code == 201
    for extra in ({"session": "lane-a"}, {}):
        answer = client.post(
            "/api/leases",
            json={"app": "cursor", "folder": "/work", "pid": 7, **extra},
            headers={"authorization": f"Bearer {key}"},
        )
        # The bridge must see a code that it can tell from a refusal of the session.
        error = answer.json().get("error") or {}
        assert error.get("code") == "MAIL_SERVER_DOWN", (
            answer.status_code,
            answer.text,
        )
        assert answer.status_code == 503
        assert error["retry_safe"] is True and error["message"]


class RefusedMail(DownMail):
    """A mail session that the mail server refuses at the login."""

    def connect(self):
        raise OSError("authentication failed")


class RefusedFactory:
    def open(self, login, secret):
        return RefusedMail()


class BrokenFactory:
    """A mail client factory with a defect."""

    def open(self, login, secret):
        raise RuntimeError("a defect")


class FailedCounter(NewMailCounter):
    async def count(self, state) -> int | None:
        raise OSError("the mail server does not reply")


def make_api(tmp_path, factory, counter):
    return create_test_app(
        Root,
        provider_overrides={
            CredentialFiles: LocalCredentialFiles(str(tmp_path / "secrets")),
            MailboxAcl: NoAcl(),
            MailClientFactory: factory,
            NewMailCounter: counter,
        },
    )


def lease_request(client, key, **extra):
    return client.post(
        "/api/leases",
        json={"app": "cursor", "folder": "/work", "pid": 7, **extra},
        headers={"authorization": f"Bearer {key}"},
    )


@pytest.mark.parametrize(
    ("factory", "status", "code", "retry_safe"),
    [
        (RefusedFactory(), 502, "LOGIN_FAILED", False),
        (BrokenFactory(), 500, "INTERNAL", True),
    ],
)
def test_each_lease_request_error_has_a_code(
    tmp_path, factory, status, code, retry_safe
):
    app = make_api(tmp_path, factory, FixedCounter())
    with AsgiTestClient(app) as client:
        anyio.run(app.get(CredentialService).init_accounts, False)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        answer = lease_request(client, key)
        assert answer.status_code == status, answer.text
        error = answer.json()["error"]
        assert (error["code"], error["retry_safe"]) == (code, retry_safe)
        assert "a defect" not in answer.text


def test_a_heartbeat_when_the_count_fails_gives_new_mail_null(tmp_path):
    app = make_api(tmp_path, FakeFactory(), FailedCounter())
    with AsgiTestClient(app) as client:
        anyio.run(app.get(CredentialService).init_accounts, False)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        token = lease_request(client, key).json()["lease"]
        beat = client.post(
            "/api/leases/current/heartbeat",
            headers={"authorization": f"Bearer {token}"},
        )
        assert beat.status_code == 200
        assert beat.json() == {"session": "cursor.s1", "new_mail": None}
