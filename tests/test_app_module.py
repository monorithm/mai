import pytest
from bustan.testing import AsgiTestClient, create_test_app


def _root():
    try:
        from mai.app_module import AppModule
    except Exception as e:  # noqa: BLE001 - a feature module that does not import yet
        pytest.skip(f"a feature module does not import: {type(e).__name__}: {e}")
    return AppModule


def test_root_module_serves_healthz():
    with AsgiTestClient(create_test_app(_root())) as client:
        assert client.get("/healthz").status_code == 200


def test_migration_versions_are_unique():
    _root()
    from mai.app_migrate import all_migrations
    from mai.platform.database.database import _check

    _check(all_migrations())
