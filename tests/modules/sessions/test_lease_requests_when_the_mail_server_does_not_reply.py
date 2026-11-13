"""Pool lease requests and binds when the mail server does not reply.

A mail server that does not reply accepts each connection and sends no byte.
Then doveadm waits for its timeout, and the mail client waits for its
timeout. The answer of the lease API and the tool result of a bind must have
the code MAIL_SERVER_DOWN, not an HTTP 504 with no code.
"""

import json
import os
import socket
import stat

import anyio
import pytest
from bustan import Module, ValueProvider
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import AgentKeys, CredentialService
from mai.modules.credentials.domain.credential_files import CredentialFiles
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
from mai.modules.credentials.infra.doveadm_mailbox_acl import DoveadmMailboxAcl
from mai.modules.credentials.infra.local_credential_files import (
    LocalCredentialFiles,
)
from mai.modules.mcp import Dispatcher
from mai.modules.mcp.api.mcp_controller import McpController
from mai.modules.mcp.api.mcp_http_policy import McpHttpPolicy
from mai.modules.mcp.application.wait_lane import WaitLane
from mai.modules.sessions import LeaseService, SessionService, SessionTools
from mai.modules.sessions.domain.lease import MAX_SESSIONS
from mai.modules.sessions.domain.new_mail_counter import NewMailCounter
from mai.platform.mailserver import MailClientFactory
from tests.modules.sessions.test_lease_service import FixedCounter
from tests.modules.sessions.test_session_tools import FakeFactory
from tests.modules.sessions.test_sessions_api import AUTH, OWNER, Root

HOST = "127.0.0.1:8080"
# A doveadm that waits while the mail server does not reply. The file
# "silent" next to it starts the wait.
FAKE_DOVEADM = """#!/bin/sh
if [ -e "$(dirname "$0")/silent" ]; then
  exec sleep 120
fi
exit 0
"""


class SilentMailServer:
    """A TCP port that accepts connections and never sends a byte."""

    def __init__(self) -> None:
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(64)
        self.port = self.sock.getsockname()[1]

    def factory(self) -> MailClientFactory:
        """The mail client of the server, with its usual timeout, on this port."""
        return MailClientFactory(
            imap_host="127.0.0.1",
            imap_port=self.port,
            smtp_host="127.0.0.1",
            smtp_port=self.port,
        )

    def close(self) -> None:
        self.sock.close()


class SwitchFactory:
    """The fake mail server until the test sets silent, then the silent mail server."""

    def __init__(self, silent: MailClientFactory) -> None:
        self.fake = FakeFactory()
        self.silent_factory = silent
        self.silent = False

    def open(self, login, secret):
        if self.silent:
            return self.silent_factory.open(login, secret)
        return self.fake.open(login, secret)


class Mail:
    """The silent mail server, the fake doveadm, and a switch between them."""

    def __init__(self, tmp_path, monkeypatch) -> None:
        self.server = SilentMailServer()
        self.factory = SwitchFactory(self.server.factory())
        self.bin = tmp_path / "bin"
        self.bin.mkdir()
        doveadm = self.bin / "doveadm"
        doveadm.write_text(FAKE_DOVEADM)
        doveadm.chmod(doveadm.stat().st_mode | stat.S_IXUSR)
        monkeypatch.setenv("PATH", f"{self.bin}{os.pathsep}{os.environ['PATH']}")

    def stop_replies(self) -> None:
        self.factory.silent = True
        (self.bin / "silent").write_text("")

    def close(self) -> None:
        self.server.close()


@pytest.fixture
def mail(tmp_path, monkeypatch):
    m = Mail(tmp_path, monkeypatch)
    yield m
    m.close()


def make_app(tmp_path, mail: Mail):
    app = create_test_app(
        Root,
        provider_overrides={
            CredentialFiles: LocalCredentialFiles(str(tmp_path / "secrets")),
            MailboxAcl: DoveadmMailboxAcl(),
            MailClientFactory: mail.factory,
            NewMailCounter: FixedCounter(),
        },
    )
    # A short time limit keeps the test fast. The server uses the same code path.
    app.get(LeaseService).request_seconds = 2.0
    return app


def lease(client, key, pid):
    return client.post(
        "/api/leases",
        json={"app": "cursor", "folder": "/work", "pid": pid},
        headers={"authorization": f"Bearer {key}"},
    )


def test_a_pool_lease_request_gives_mail_server_down_when_the_mail_server_does_not_reply(
    tmp_path, mail
):
    app = make_app(tmp_path, mail)
    with AsgiTestClient(app) as client:
        credentials = app.get(CredentialService)
        anyio.run(credentials.init_accounts, False)
        anyio.run(credentials.owner_set_key, OWNER)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        made = client.post(
            "/api/sessions", json={"agent": "cursor", "count": 1}, headers=AUTH
        )
        assert made.status_code == 201
        mail.stop_replies()
        answer = lease(client, key, 7)
    body = answer.json()
    error = body.get("error") if isinstance(body.get("error"), dict) else {}
    # The lease API gives a coded error, in the form {"error": {"code", "message"}}.
    assert (answer.status_code, error.get("code")) == (503, "MAIL_SERVER_DOWN"), (
        answer.status_code,
        answer.text,
    )
    assert error["retry_safe"] is True


def test_a_bind_gives_the_tool_error_mail_server_down_when_the_mail_server_does_not_reply(
    tmp_path, mail
):
    app = make_app(tmp_path, mail)
    with AsgiTestClient(app) as client:
        credentials = app.get(CredentialService)
        anyio.run(credentials.init_accounts, False)
        anyio.run(credentials.owner_set_key, OWNER)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        pool = [lease(client, key, pid).json() for pid in range(MAX_SESSIONS)]
        extra = lease(client, key, 9).json()
        assert extra["session"] is None
        ended = client.delete(
            "/api/leases/current",
            headers={"authorization": f"Bearer {pool[0]['lease']}"},
        )
        assert ended.status_code == 204
        sessions = app.get(SessionService)
        leases = app.get(LeaseService)
        dispatcher = Dispatcher(
            dict(SessionTools(sessions).tools()),
            sessions.resolve,
            leases.state,
            leases.check,
        )

    @Module(
        controllers=[McpController],
        providers=[
            ValueProvider(provide=Dispatcher, use_value=dispatcher),
            ValueProvider(
                provide=McpHttpPolicy,
                use_value=McpHttpPolicy(frozenset({HOST}), frozenset()),
            ),
            ValueProvider(provide=WaitLane, use_value=WaitLane()),
        ],
    )
    class BindMcpModule:
        pass

    mail.stop_replies()
    call = {
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {"name": "whoami", "arguments": {}},
    }
    with AsgiTestClient(create_test_app(BindMcpModule)) as client:
        answer = client.post(
            "/mcp",
            content=json.dumps(call),
            headers={
                "host": HOST,
                "content-type": "application/json",
                "authorization": f"Bearer {extra['lease']}",
            },
        )
    # A bind failure is a tool result with HTTP 200, with the code of the mail login.
    assert answer.status_code == 200, (answer.status_code, answer.text)
    result = answer.json()["result"]
    assert result["isError"] is True
    assert result["structuredContent"]["error"]["code"] == "MAIL_SERVER_DOWN"
    assert result["structuredContent"]["error"]["retry_safe"] is True
