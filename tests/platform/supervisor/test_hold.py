import json
import threading
import time

from mai.platform import supervisor


def test_hold_and_release_round_trip(tmp_path):
    f = tmp_path / "hold"
    assert supervisor.held(f) == set()
    supervisor.hold(("postfix", "dovecot"), f)
    assert supervisor.held(f) == {"postfix", "dovecot"}
    supervisor.release(f)
    supervisor.release(f)
    assert supervisor.held(f) == set()


def test_wait_restarted_needs_a_new_process(tmp_path):
    state = tmp_path / "state.json"
    state.write_text(json.dumps({"postfix": {"running": True, "pid": 10}}))
    assert (
        supervisor.wait_restarted(
            {"postfix": 10}, timeout=0.3, poll=0.05, state_file=state
        )
        is False
    )

    def restart():
        time.sleep(0.1)
        state.write_text(json.dumps({"postfix": {"running": True, "pid": 11}}))

    threading.Thread(target=restart).start()
    assert (
        supervisor.wait_restarted(
            {"postfix": 10}, timeout=2, poll=0.05, state_file=state
        )
        is True
    )


def test_a_hold_of_a_stopped_command_is_not_valid(tmp_path):
    import subprocess
    import sys

    f = tmp_path / "hold"
    done = subprocess.run(
        [sys.executable, "-c", "import os; print(os.getpid())"],
        capture_output=True,
        text=True,
        check=True,
    )
    dead_pid = int(done.stdout)
    supervisor.hold(("postfix",), f, owner=dead_pid)
    assert supervisor.held(f) == set()
    assert supervisor.stale(f) is True
    supervisor.hold(("postfix",), f)
    assert supervisor.held(f) == {"postfix"}
    assert supervisor.stale(f) is False


def test_exclusive_refuses_a_second_holder(tmp_path):
    import pytest

    lock = tmp_path / "restore.lock"
    with (
        supervisor.exclusive(lock),
        pytest.raises(supervisor.HoldBusy),
        supervisor.exclusive(lock),
    ):
        pass
    with supervisor.exclusive(lock):
        pass


def test_wait_stopped(tmp_path):
    state = tmp_path / "state.json"
    state.write_text(json.dumps({"app": {"running": True, "pid": 5}}))
    assert (
        supervisor.wait_stopped(["app"], timeout=0.2, poll=0.05, state_file=state)
        is False
    )
    state.write_text(json.dumps({"app": {"running": False, "pid": None}}))
    assert (
        supervisor.wait_stopped(["app"], timeout=0.2, poll=0.05, state_file=state)
        is True
    )


def test_supervised_from_the_start_of_the_controller(tmp_path):
    import os

    pid_file = tmp_path / "controller.pid"
    state = tmp_path / "state.json"
    assert supervisor.supervised(state, pid_file) is False
    supervisor.mark_controller(pid_file)
    assert pid_file.read_text().strip() == str(os.getpid())
    assert supervisor.supervised(state, pid_file) is True
