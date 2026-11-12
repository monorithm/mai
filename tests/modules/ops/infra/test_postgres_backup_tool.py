import json
import os
import threading
import time

import pytest

from mai.modules.ops.infra import postgres_backup_tool as tool
from mai.platform import supervisor
from mai.platform.supervisor import protocol as real


@pytest.fixture
def controller(tmp_path, monkeypatch):
    """A fake controller: it stops the held daemons and starts them with a new PID after the hold."""
    state = tmp_path / "state.json"
    hold = tmp_path / "hold"
    lock = tmp_path / "restore.lock"
    pids = {"app": 1, "postfix": 2, "dovecot": 3}
    state.write_text(
        json.dumps({n: {"running": True, "pid": p} for n, p in pids.items()})
    )
    monkeypatch.setattr(supervisor, "supervised", lambda: True)
    monkeypatch.setattr(supervisor, "wait_ready", lambda: True)
    monkeypatch.setattr(supervisor, "states", lambda: json.loads(state.read_text()))
    monkeypatch.setattr(supervisor, "hold", lambda names: real.hold(names, hold))
    monkeypatch.setattr(supervisor, "release", lambda: hold.unlink(missing_ok=True))
    monkeypatch.setattr(supervisor, "exclusive", lambda: real.exclusive(lock))
    real_stopped, real_restarted = supervisor.wait_stopped, supervisor.wait_restarted
    monkeypatch.setattr(
        supervisor, "wait_stopped", lambda n: real_stopped(n, 5, 0.02, state)
    )
    monkeypatch.setattr(
        supervisor, "wait_restarted", lambda p: real_restarted(p, 5, 0.02, state)
    )
    stop = threading.Event()

    def loop():
        while not stop.is_set():
            names, _owner = supervisor.read_hold(hold)
            now = {}
            for n, pid in pids.items():
                if n in names:
                    now[n] = {"running": False, "pid": None}
                else:
                    now[n] = {"running": True, "pid": pid + (100 if seen_hold else 0)}
            # Atomic, as the controller writes it: a reader never sees a partial file.
            tmp = state.with_suffix(".tmp")
            tmp.write_text(json.dumps(now))
            tmp.replace(state)
            time.sleep(0.01)

    seen_hold = False

    def watch():
        nonlocal seen_hold
        while not stop.is_set():
            if hold.exists():
                seen_hold = True
            time.sleep(0.005)

    threads = [threading.Thread(target=loop), threading.Thread(target=watch)]
    for t in threads:
        t.start()
    yield {"hold": hold, "lock": lock}
    stop.set()
    for t in threads:
        t.join()


def test_restore_holds_app_postfix_and_dovecot_and_waits_for_the_controller(
    controller, monkeypatch
):
    ran = []
    monkeypatch.setattr(tool.subprocess, "run", lambda argv, **kw: ran.append(argv))
    tool._daemons("stop")
    names, owner = supervisor.read_hold(controller["hold"])
    assert names == {"app", "postfix", "dovecot"} and owner == os.getpid()
    tool._daemons("start")
    assert not controller["hold"].exists()
    # The controller stops and starts the daemons: the restore runs no daemon command.
    assert ran == []


def test_a_second_restore_at_the_same_time_gets_hold_busy(controller):
    tool._daemons("stop")
    try:
        with pytest.raises(supervisor.HoldBusy), supervisor.exclusive():
            pass
    finally:
        tool._daemons("start")
