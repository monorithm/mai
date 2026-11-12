"""The owner HTTP API of the agents and their keys, on the memory backend."""

import anyio
import pytest
from bustan import ConfigModule, Module
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import AgentKeys, CredentialService, CredentialsModule
from mai.modules.credentials.domain.agent_key_store import AgentKeyStore
from mai.modules.credentials.domain.credential_files import CredentialFiles
from mai.modules.credentials.domain.errors import CredentialError
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
from mai.modules.credentials.infra.in_memory_agent_key_store import (
    InMemoryAgentKeyStore,
)
from mai.modules.credentials.infra.local_credential_files import (
    LocalCredentialFiles,
)


class NoAcl(MailboxAcl):
    async def grant(self, agent, login, rights):
        return False

    async def remove(self, agent, login):
        return False


@Module(imports=[ConfigModule.for_root(), CredentialsModule])
class Root:
    pass


@pytest.fixture
def client(tmp_path):
    files = LocalCredentialFiles(str(tmp_path / "secrets"))
    store = InMemoryAgentKeyStore()
    app = create_test_app(
        Root,
        provider_overrides={
            CredentialFiles: files,
            MailboxAcl: NoAcl(),
            AgentKeyStore: store,
        },
    )
    with AsgiTestClient(app) as test_client:
        service = app.get(CredentialService)
        anyio.run(service.init_accounts)
        owner = files.read(files.system_path("owner"))
        yield test_client, app, store, {"authorization": f"Bearer {owner}"}


def test_agents_need_the_owner_key(client):
    test_client, _app, _store, _auth = client
    response = test_client.get("/api/agents")
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "BAD_KEY"
    bad = {"authorization": "Bearer mai1:owner:" + "a" * 52}
    assert (
        test_client.post("/api/agents", json={"name": "x"}, headers=bad).status_code
        == 401
    )


def test_add_list_rotate_and_remove_an_agent(client):
    test_client, app, store, auth = client
    keys = app.get(AgentKeys)

    made = test_client.post("/api/agents", json={"name": "cursor"}, headers=auth)
    assert made.status_code == 201
    body = made.json()
    assert body["name"] == "cursor"
    first = body["agent_key"]
    assert first.startswith("mai1:cursor:")
    stored = anyio.run(store.get_hash, "cursor")
    assert stored is not None and first.split(":")[2] not in stored
    assert anyio.run(keys.check, first) == "cursor"

    again = test_client.post("/api/agents", json={"name": "cursor"}, headers=auth)
    assert again.status_code == 409
    assert again.json()["error"]["code"] == "AGENT_EXISTS"
    bad = test_client.post("/api/agents", json={"name": "Bad Name"}, headers=auth)
    assert bad.status_code == 400

    listed = test_client.get("/api/agents", headers=auth).json()
    assert listed == {"agents": [{"name": "cursor", "sessions": 0, "leases": 0}]}

    rotated = test_client.post("/api/agents/cursor/key", headers=auth)
    assert rotated.status_code == 200
    second = rotated.json()["agent_key"]
    assert second != first
    assert anyio.run(keys.check, second) == "cursor"
    with pytest.raises(CredentialError):
        anyio.run(keys.check, first)
    assert test_client.post("/api/agents/nobody/key", headers=auth).status_code == 404

    assert test_client.delete("/api/agents/cursor", headers=auth).status_code == 204
    assert test_client.delete("/api/agents/cursor", headers=auth).status_code == 404
    with pytest.raises(CredentialError):
        anyio.run(keys.check, second)
    assert test_client.get("/api/agents", headers=auth).json() == {"agents": []}

    readded = test_client.post("/api/agents", json={"name": "cursor"}, headers=auth)
    assert readded.status_code == 201


def test_a_bad_agent_key_gives_bad_key(client):
    _test_client, app, _store, _auth = client
    keys = app.get(AgentKeys)
    for key in ("", "mai1:cursor:short", "mai1:owner:" + "a" * 52, 7):
        with pytest.raises(CredentialError) as info:
            anyio.run(keys.check, key)
        assert info.value.code == "BAD_KEY"
