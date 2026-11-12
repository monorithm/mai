import datetime as dt
from typing import cast

import anyio
from bustan import ConfigModule, ConfigService, Module
from bustan.testing import create_testing_module

from mai.modules.ops import OpsModule, OpsService
from mai.modules.ops.jobs.daily_backup_job import DailyBackupHandler
from mai.platform.clock import Clock, FixedClock


class _Config(ConfigService):
    def __init__(self):
        super().__init__({})

    def get(self, key: str, default=None):
        return default


@Module(imports=[ConfigModule.for_root(), OpsModule])
class _Root:
    pass


def test_ops_module_builds_and_schedules_at_two_utc():
    async def scenario():
        clock = FixedClock(dt.datetime(2026, 11, 12, 1, 0, tzinfo=dt.UTC))
        compiled = (
            await create_testing_module(_Root)
            .override_provider(Clock)
            .use_value(clock)
            .compile()
        )
        try:
            ops = compiled.get(OpsService)
            assert isinstance(ops, OpsService)
            handler = DailyBackupHandler(ops, clock, _Config())
            assert handler.seconds_to_next_run() == 3600
            handler.on_application_bootstrap()  # disabled: no task starts
            assert handler._task is None
        finally:
            await compiled.close()

    anyio.run(scenario)


class _Value(ConfigService):
    def __init__(self, value):
        super().__init__({})
        self.value = value

    def get(self, key: str, default=None):
        return default if self.value is None else self.value


def test_daily_backup_job_of_the_app_runs_only_with_the_explicit_switch(monkeypatch):
    # In the container the controller runs the backup as root: the app user cannot do it.
    clock = FixedClock(dt.datetime(2026, 11, 12, 1, 0, tzinfo=dt.UTC))
    monkeypatch.setenv("MAI_BACKGROUND", "on")
    # The switch check does not use the ops service, so no service is given.
    no_ops = cast(OpsService, None)
    assert DailyBackupHandler(no_ops, clock, _Value(None))._enabled is False
    assert DailyBackupHandler(no_ops, clock, _Value("true"))._enabled is True
