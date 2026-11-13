"""GET /api/version with no key and GET /api/health with the owner key."""

import anyio
from bustan import ConfigModule, HealthModule, Module
from bustan.testing import AsgiTestClient, create_test_app

from mai.modules.credentials import CredentialService, CredentialsModule
from mai.modules.mcp import McpModule

OWNER = "mai1:owner:" + "o" * 52


@Module(
    imports=[
        ConfigModule.for_root(),
        HealthModule.for_root(),
        McpModule,
        CredentialsModule,
    ]
)
class Root:
    pass


def test_version_needs_no_key_and_health_needs_the_owner_key():
    app = create_test_app(Root)
    with AsgiTestClient(app) as client:
        version = client.get("/api/version")
        assert version.status_code == 200
        assert version.json() == {"version": "3.1.1", "contract": "3.2.0"}
        refused = client.get("/api/health")
        assert refused.status_code == 401
        assert refused.json()["error"]["code"] == "BAD_KEY"
        service = app.get(CredentialService)
        anyio.run(service.owner_set_key, OWNER)
        health = client.get("/api/health", headers={"authorization": f"Bearer {OWNER}"})
        assert health.status_code == 200
        assert set(health.json()) == {"healthy", "children"}
        assert "postgres" in health.json()["children"]
