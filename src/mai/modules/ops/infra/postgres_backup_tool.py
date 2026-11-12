"""Dumps and restores the PostgreSQL database, and stops and starts the mail daemons."""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path
from typing import ClassVar

import anyio.to_thread
from bustan import Injectable

from mai.platform import supervisor

from ..domain.ports import BackupTool

DB_NAME = "mai"
PG_SOCKET_DIR = "/run/postgresql"


def _superuser() -> str:
    return os.environ.get("MAI_PG_SUPERUSER", "postgres")


def _dump(target: Path) -> None:
    with open(target, "wb") as fh:
        subprocess.run(
            ["pg_dump", "-h", PG_SOCKET_DIR, "-U", _superuser(), "-Fc", DB_NAME],
            stdout=fh,
            check=True,
        )


def _die_with_parent() -> None:
    """In the child: the kernel sends SIGKILL to it when its parent stops (Linux PR_SET_PDEATHSIG)."""
    try:
        import ctypes
        import signal as _signal

        ctypes.CDLL(None).prctl(1, int(_signal.SIGKILL))
    except Exception:  # noqa: BLE001, S110 - on a host with no prctl the restore still runs
        pass


def _restore(source: Path) -> None:
    subprocess.run(
        [
            "pg_restore",
            "-h",
            PG_SOCKET_DIR,
            "-U",
            _superuser(),
            "--clean",
            "--if-exists",
            # One transaction: an error leaves the database as it was before the restore.
            "--single-transaction",
            "--exit-on-error",
            "-d",
            DB_NAME,
            str(source),
        ],
        check=True,
        # The restore process starts no threads, thus a preexec function is safe here.
        preexec_fn=_die_with_parent,
    )


# The children of the controller that a restore stops: the app writes to the
# database, and Postfix and Dovecot write to the mail files.
HELD_DAEMONS = ("app", "postfix", "dovecot")


def _daemons(action: str) -> None:
    """Stop or start the daemons around a restore.

    In the container, the controller supervises the daemons: the restore puts
    them on hold, and the controller stops them and starts them again (see
    mai.platform.supervisor). With no controller, the commands stop and
    start Postfix and Dovecot directly. Errors are ignored on a direct stop.
    """
    if supervisor.supervised():
        _supervised_daemons(action)
        return
    cmds = {
        "stop": [["postfix", "stop"], ["doveadm", "stop"]],
        "start": [["dovecot"], ["postfix", "start"]],
    }[action]
    for c in cmds:
        if shutil.which(c[0]):
            subprocess.run(
                c,
                check=(action == "start"),
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )


class _Hold:
    """The hold of one restore: the exclusive lock and the PIDs before the stop."""

    lock: ClassVar = None
    old_pids: ClassVar[dict[str, int | None]] = {}


def _supervised_daemons(action: str) -> None:
    if action == "stop":
        if not supervisor.wait_ready():
            raise RuntimeError("the controller is not ready: try the restore again")
        lock = supervisor.exclusive()
        lock.__enter__()
        _Hold.lock = lock
        now = supervisor.states()
        _Hold.old_pids = {n: now.get(n, {}).get("pid") for n in HELD_DAEMONS}
        supervisor.hold(HELD_DAEMONS)
        if not supervisor.wait_stopped(HELD_DAEMONS):
            _end_hold()
            raise RuntimeError(
                "the controller did not stop the daemons for the restore"
            )
        return
    try:
        supervisor.release()
        if not supervisor.wait_restarted(dict(_Hold.old_pids)):
            raise RuntimeError("the controller did not start the daemons again")
    finally:
        _end_hold()


def _end_hold() -> None:
    supervisor.release()
    lock, _Hold.lock = _Hold.lock, None
    if lock is not None:
        lock.__exit__(None, None, None)


@Injectable()
class PostgresBackupTool(BackupTool):
    async def dump_database(self, target: Path) -> None:
        await anyio.to_thread.run_sync(_dump, target)

    async def restore_database(self, source: Path) -> None:
        await anyio.to_thread.run_sync(_restore, source)

    def exclusive(self):
        """The restore lock of the controller: a backup waits for no restore, it refuses."""
        if supervisor.supervised():
            return supervisor.exclusive()
        return super().exclusive()

    async def stop_daemons(self) -> None:
        await anyio.to_thread.run_sync(_daemons, "stop")

    async def start_daemons(self) -> None:
        await anyio.to_thread.run_sync(_daemons, "start")
