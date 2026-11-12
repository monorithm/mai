"""The daily backup at 02:00 UTC. The backup keeps the newest 14."""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass

from bustan import ConfigService, Injectable

from mai.platform.clock import Clock

from ..application.ops_service import OpsService
from ..domain.backups import BACKUP_HOUR_UTC, next_backup_at

log = logging.getLogger("mai.ops")


@dataclass(frozen=True, slots=True)
class DailyBackupJob:
    hour_utc: int = BACKUP_HOUR_UTC


@Injectable()
class DailyBackupHandler:
    """Runs the backup once a day while the process runs.

    The schedule starts only when OPS_BACKUP_SCHEDULE is true. In the
    container, the controller runs the daily backup as root, because the app
    user cannot read the mail files or write /data/backup. Thus the container
    does not set OPS_BACKUP_SCHEDULE, and this job is for a process that runs
    as a user with those rights.
    """

    def __init__(self, ops: OpsService, clock: Clock, config: ConfigService) -> None:
        self._ops = ops
        self._clock = clock
        self._enabled = str(config.get("OPS_BACKUP_SCHEDULE", "false")).lower() in (
            "1",
            "true",
            "yes",
        )
        self._task: asyncio.Task | None = None
        self.job = DailyBackupJob()

    def seconds_to_next_run(self) -> float:
        now = self._clock.now()
        return max(0.0, (next_backup_at(now, self.job.hour_utc) - now).total_seconds())

    async def run(self, job: DailyBackupJob) -> dict:
        return await self._ops.backup()

    async def _loop(self) -> None:
        while True:
            await asyncio.sleep(self.seconds_to_next_run())
            try:
                result = await self.run(self.job)
                log.info(
                    "backup %s written, removed %d",
                    result["stamp"],
                    len(result["removed"]),
                )
            except Exception as exc:  # noqa: BLE001 - the next day tries again
                log.error("backup failed: %s", type(exc).__name__)
            await asyncio.sleep(1)

    def on_application_bootstrap(self) -> None:
        if self._enabled and self._task is None:
            self._task = asyncio.get_running_loop().create_task(self._loop())

    async def on_application_shutdown(self, signal: str | None = None) -> None:
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except (asyncio.CancelledError, Exception):  # noqa: BLE001, S110
                pass
            self._task = None
