"""The owner HTTP API of the session logins, on the memory backend."""

import pytest
from bustan import ConfigModule, Module
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import CredentialsModule
from mai.modules.credentials.domain.credential_files import CredentialFiles
from mai.modules.credentials.domain.mailbox_acl import MailboxAcl
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
    app = create_test_app(
        Root, provider_overrides={CredentialFiles: files, MailboxAcl: NoAcl()}
    )
    with AsgiTestClient(app) as test_client:
        yield test_client, app, files


def test_owner_api(client):
    test_client, app, _files = client
    import anyio

    from mai.modules.credentials import CredentialService

    service = app.get(CredentialService) if hasattr(app, "get") else None
    if service is None:
        pytest.skip("the test app gives no provider lookup")
    anyio.run(service.init_accounts)
    anyio.run(service.agent_add, "cursor")
    owner = "mai1:owner:" + "o" * 52
    anyio.run(service.owner_set_key, owner)
    auth = {"authorization": f"Bearer {owner}"}

    assert test_client.post("/api/sessions/cursor.a/rotate").status_code == 401
    assert (
        test_client.post(
            "/api/sessions/cursor.a/rotate",
            headers={"authorization": "Bearer mai1:x:y"},
        ).status_code
        == 401
    )
    made = test_client.post(
        "/api/sessions", json={"agent": "cursor", "label": "a"}, headers=auth
    )
    assert made.status_code == 201
    assert made.json()["login"] == "cursor.a"
    assert (
        test_client.post(
            "/api/sessions", json={"agent": "cursor", "label": "a"}, headers=auth
        ).status_code
        == 409
    )
    assert (
        test_client.post(
            "/api/sessions", json={"agent": "nobody", "label": "a"}, headers=auth
        ).status_code
        == 400
    )
    listed = anyio.run(service.session_list)
    assert [s["login"] for s in listed] == ["cursor.a"]
    assert (
        test_client.post("/api/sessions/cursor.a/rotate", headers=auth).status_code
        == 200
    )
    assert (
        test_client.post("/api/sessions/cursor.b/rotate", headers=auth).status_code
        == 404
    )
    revoked = test_client.post("/api/sessions/cursor.a/revoke", headers=auth)
    assert revoked.json() == {"login": "cursor.a", "status": "revoked"}
