from bustan import ConfigModule, HealthModule, Module
from bustan.testing import AsgiTestClient, create_test_app

from mai.app_settings import AppSettings
from mai.platform.health import HealthChecksModule


@Module(
    imports=[
        ConfigModule.for_root(ignore_env_file=True, validation_schema=AppSettings),
        HealthModule.for_root(check_timeout=2.0),
        HealthChecksModule,
    ]
)
class HealthRoot:
    pass


def test_healthz_answers_on_the_memory_backend():
    with AsgiTestClient(create_test_app(HealthRoot)) as client:
        r = client.get("/healthz")
        assert r.status_code == 200
        assert r.json() == {"ok": True}
        assert client.get("/health/live").status_code == 200
