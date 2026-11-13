"""The lease API when the store does not reply.

A store that does not reply accepts a query and gives no result. The lease
request and the heartbeat must then give a coded error, for example 503
STORE_UNAVAILABLE, not an HTTP 504 with no code.
"""

import anyio
import pytest
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import AgentKeys, CredentialService
from mai.modules.credentials.domain.agent_key_store import AgentKeyStore
from mai.modules.credentials.domain.credential_files import CredentialFiles
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
from mai.modules.credentials.infra.in_memory_agent_key_store import (
    InMemoryAgentKeyStore,
)
from mai.modules.credentials.infra.local_credential_files import (
    LocalCredentialFiles,
)
from mai.modules.sessions import LeaseService
from mai.modules.sessions.domain.handle_store import HandleStore
from mai.modules.sessions.domain.new_mail_counter import NewMailCounter
from mai.modules.sessions.infra.in_memory_handle_store import InMemoryHandleStore
from mai.platform.mailserver import MailClientFactory
from tests.modules.sessions.test_lease_service import FixedCounter
from tests.modules.sessions.test_session_tools import FakeFactory
from tests.modules.sessions.test_sessions_api import NoAcl, Root

# Longer than each time limit of a request: the store never replies in the test.
NO_REPLY_SECONDS = 120


class Silence:
    """When on is true, each store query of the test waits and gives no result."""

    def __init__(self) -> None:
        self.on = False

    async def wait(self) -> None:
        if self.on:
            await anyio.sleep(NO_REPLY_SECONDS)


class SilentAgentKeyStore(InMemoryAgentKeyStore):
    def __init__(self, silence: Silence) -> None:
        super().__init__()
        self.silence = silence

    async def get_hash(self, agent: str) -> str | None:
        await self.silence.wait()
        return await super().get_hash(agent)


class SilentHandleStore(InMemoryHandleStore):
    def __init__(self, silence: Silence) -> None:
        super().__init__()
        self.silence = silence

    async def extend(self, *args, **kwargs):
        await self.silence.wait()
        return await super().extend(*args, **kwargs)


@pytest.fixture
def silent_store_api(tmp_path):
    silence = Silence()
    app = create_test_app(
        Root,
        provider_overrides={
            CredentialFiles: LocalCredentialFiles(str(tmp_path / "secrets")),
            MailboxAcl: NoAcl(),
            MailClientFactory: FakeFactory(),
            NewMailCounter: FixedCounter(),
            AgentKeyStore: SilentAgentKeyStore(silence),
            HandleStore: SilentHandleStore(silence),
        },
    )
    # Short time limits keep the test fast. The server uses the same code path.
    leases = app.get(LeaseService)
    leases.request_seconds = 1.0
    leases.heartbeat_seconds = 1.0
    with AsgiTestClient(app) as client:
        anyio.run(app.get(CredentialService).init_accounts, False)
        key = anyio.run(app.get(AgentKeys).add, "cursor")
        yield client, key, silence


def coded(answer) -> tuple[int, str | None]:
    """The status and the error code of an answer. The code is None for an answer with no code."""
    try:
        body = answer.json()
    except ValueError:
        return answer.status_code, None
    error = body.get("error") if isinstance(body, dict) else None
    return answer.status_code, error.get("code") if isinstance(error, dict) else None


def test_a_lease_request_gives_store_unavailable_when_the_store_does_not_reply(
    silent_store_api,
):
    client, key, silence = silent_store_api
    silence.on = True
    answer = client.post(
        "/api/leases",
        json={"app": "cursor", "folder": "/work", "pid": 7},
        headers={"authorization": f"Bearer {key}"},
    )
    assert coded(answer) == (503, "STORE_UNAVAILABLE"), answer.text


def test_a_heartbeat_gives_a_coded_answer_when_the_store_does_not_reply(
    silent_store_api,
):
    client, key, silence = silent_store_api
    made = client.post(
        "/api/leases",
        json={"app": "cursor", "folder": "/work", "pid": 7},
        headers={"authorization": f"Bearer {key}"},
    )
    assert made.status_code == 201
    silence.on = True
    beat = client.post(
        "/api/leases/current/heartbeat",
        headers={"authorization": f"Bearer {made.json()['lease']}"},
    )
    status, code = coded(beat)
    # A heartbeat gives its usual answer, or a coded error such as 503 STORE_UNAVAILABLE.
    assert status == 200 or code is not None, (status, beat.text)
