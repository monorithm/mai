from __future__ import annotations

import contextlib
import json
import os
import time
from pathlib import Path

HOLD_FILE = Path(os.environ.get("MAI_HOLD_FILE", "/run/mai/hold"))
STATE_FILE = Path(os.environ.get("MAI_STATE_FILE", "/run/mai/state.json"))
LOCK_FILE = Path(os.environ.get("MAI_RESTORE_LOCK", "/run/mai/restore.lock"))
CONTROLLER_FILE = Path(os.environ.get("MAI_CONTROLLER_FILE", "/run/mai/controller.pid"))


class HoldBusy(RuntimeError):
    """A different command holds the daemons at this time."""


def supervised(
    state_file: Path = STATE_FILE, controller_file: Path = CONTROLLER_FILE
) -> bool:
    """True when the container controller runs: from its start, also before its first state file."""
    try:
        pid = int(controller_file.read_text().strip())
    except (OSError, ValueError):
        return state_file.exists()
    return _alive(pid)


def mark_controller(controller_file: Path = CONTROLLER_FILE) -> None:
    """The controller writes its PID at its start."""
    controller_file.parent.mkdir(parents=True, exist_ok=True)
    controller_file.write_text(f"{os.getpid()}\n")


def wait_ready(
    timeout: float = 180.0, poll: float = 0.5, state_file: Path = STATE_FILE
) -> bool:
    """Wait for the first state file of the controller, at the start of the container."""
    return _wait(state_file.exists, timeout, poll)


def states(state_file: Path = STATE_FILE) -> dict:
    """The state of each daemon, as the controller last wrote it."""
    try:
        return json.loads(state_file.read_text())
    except (OSError, ValueError):
        return {}


def _alive(pid: int) -> bool:
    if os.name != "posix":
        # On Windows, os.kill with signal 0 stops the process. The hold protocol is for Linux only.
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def read_hold(hold_file: Path = HOLD_FILE) -> tuple[set[str], int | None]:
    """The held names and the PID of the command that holds them. No file gives (set(), None)."""
    try:
        data = json.loads(hold_file.read_text())
        return set(data.get("names", [])), int(data["owner"])
    except (OSError, ValueError, KeyError, TypeError):
        return set(), None


def held(hold_file: Path = HOLD_FILE) -> set[str]:
    """The names that the controller must not start. A hold of a stopped command is not valid."""
    names, owner = read_hold(hold_file)
    if owner is None or not _alive(owner):
        return set()
    return names


def stale(hold_file: Path = HOLD_FILE) -> bool:
    """True when a hold file exists, but the command that wrote it does not run."""
    if not hold_file.exists():
        return False
    _names, owner = read_hold(hold_file)
    return owner is None or not _alive(owner)


def hold(names, hold_file: Path = HOLD_FILE, owner: int | None = None) -> None:
    """Tell the controller to stop these daemons and not to start them until release()."""
    tmp = hold_file.with_suffix(".tmp")
    tmp.write_text(json.dumps({"owner": owner or os.getpid(), "names": list(names)}))
    tmp.replace(hold_file)


def release(hold_file: Path = HOLD_FILE) -> None:
    """Let the controller start the held daemons again."""
    hold_file.unlink(missing_ok=True)


@contextlib.contextmanager
def exclusive(lock_file: Path = LOCK_FILE):
    """Only one command at a time can hold the daemons. A second one gets HoldBusy at once."""
    fd = os.open(lock_file, os.O_RDWR | os.O_CREAT, 0o600)
    try:
        import fcntl  # POSIX only: the controller and the restore run in the Linux container

        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise HoldBusy("a different restore runs at this time") from None
        yield
    finally:
        os.close(fd)


def _wait(test, timeout: float, poll: float) -> bool:
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if test():
            return True
        time.sleep(poll)
    return False


def wait_stopped(
    names, timeout: float = 90.0, poll: float = 0.5, state_file: Path = STATE_FILE
) -> bool:
    """Wait until the state file shows that no named daemon runs."""
    return _wait(
        lambda: all(not states(state_file).get(n, {}).get("running") for n in names),
        timeout,
        poll,
    )


def wait_restarted(
    old_pids: dict[str, int | None],
    timeout: float = 90.0,
    poll: float = 0.5,
    state_file: Path = STATE_FILE,
) -> bool:
    """Wait until each named daemon runs with a process that is not the old one."""

    def test() -> bool:
        now = states(state_file)
        return all(
            now.get(name, {}).get("running") and now[name].get("pid") != pid
            for name, pid in old_pids.items()
        )

    return _wait(test, timeout, poll)
