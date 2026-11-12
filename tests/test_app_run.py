import os
import subprocess
import sys
import time

import pytest

from mai import app_run as controller


def test_child_restarts_with_growing_backoff():
    c = controller.Child("x", [sys.executable, "-c", "pass"])
    c.start()
    assert c.proc is not None
    c.proc.wait()
    c.check(100.0)
    assert c.restarts == 1 and c.proc is None and c.next_start == 101.0
    assert c.backoff == 2.0
    c.check(101.0)
    assert c.proc is not None
    c.proc.wait()
    t = 200.0
    for _ in range(10):
        c.check(t)  # notices the stop and schedules a start
        t += 100.0
        c.check(t)  # starts again
        c.proc.wait()
    assert c.backoff == controller.MAX_BACKOFF


def test_child_stop_terminates():
    c = controller.Child("sleep", [sys.executable, "-c", "import time; time.sleep(60)"])
    c.start()
    assert c.alive()
    c.stop(timeout=5)
    assert not c.alive()


def test_stop_order():
    assert controller.STOP_ORDER == ("app", "postfix", "milter", "dovecot", "postgres")


def test_health_unhealthy_without_server(tmp_path, monkeypatch):
    monkeypatch.setattr(controller, "STATE_FILE", tmp_path / "state.json")
    assert controller.health(url="http://127.0.0.1:9/healthz", timeout=0.5) == 1
    (tmp_path / "state.json").write_text('{"app": {"running": false}}')
    assert controller.health(url="http://127.0.0.1:9/healthz", timeout=0.5) == 1


def test_config_templates_exist():
    root = os.path.join(os.path.dirname(__file__), "..", "etc")
    for f in (
        "postfix/main.cf",
        "postfix/master.cf",
        "postfix/pgsql-sender-login.cf",
        "postfix/pgsql-mailbox.cf",
        "postfix/pgsql-alias.cf",
        "dovecot/dovecot.conf",
    ):
        assert os.path.exists(os.path.join(root, f)), f


@pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="needs the mai:dev image")
def test_image_daemons_check():
    out = subprocess.run(
        [
            "docker",
            "exec",
            "mai-test-image",
            "sh",
            "-c",
            "doveconf -n >/dev/null && postfix check",
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    assert out.returncode == 0, out.stderr


def test_children_include_the_milter_and_the_bustan_app():
    kids = controller.children()
    assert set(kids) == set(controller.START_ORDER)
    assert "mai.app_main" in kids["app"].argv
    assert kids["milter"].argv[:2] == ["gosu", "postfix"]
    assert str(controller.MILTER_SOCKET) in kids["milter"].argv


def test_cli_has_all_commands():
    from mai import app_cli

    parser = app_cli.build_parser()
    sub = next(a for a in parser._actions if a.dest == "command")
    assert sub.choices is not None
    assert set(sub.choices) >= {
        "run",
        "health",
        "init",
        "agent",
        "session",
        "mcp",
        "serve-http",
        "watch",
        "hook",
        "backup",
        "restore",
        "import-maildir",
        "milter",
    }


def test_a_held_child_is_not_started_again_and_starts_at_once_after_the_hold():
    c = controller.Child("short", [sys.executable, "-c", "pass"])
    c.start()
    assert c.proc is not None
    c.proc.wait()
    c.backoff = 8.0
    c.check_held()
    assert c.proc is None and c.backoff == 1.0 and c.next_start == 0.0
    c.check_held()
    assert c.proc is None
    c.check(time.monotonic())
    assert c.proc is not None
    c.proc.wait()


def test_daily_backup_runs_once_a_day_after_two_utc_and_not_at_a_late_start(
    monkeypatch,
):
    import datetime as dt

    utc = dt.UTC
    late = controller.DailyBackup(dt.datetime(2026, 11, 12, 10, 0, tzinfo=utc))
    assert late.due(dt.datetime(2026, 11, 12, 23, 0, tzinfo=utc)) is False
    assert late.due(dt.datetime(2026, 11, 13, 1, 59, tzinfo=utc)) is False
    assert late.due(dt.datetime(2026, 11, 13, 2, 0, tzinfo=utc)) is True

    early = controller.DailyBackup(dt.datetime(2026, 11, 12, 1, 0, tzinfo=utc))
    started = []
    monkeypatch.setattr(
        controller.subprocess,
        "Popen",
        lambda argv, **kw: started.append(argv) or _Done(),
    )
    early.check(dt.datetime(2026, 11, 12, 2, 0, tzinfo=utc))
    early.check(dt.datetime(2026, 11, 12, 3, 0, tzinfo=utc))
    assert len(started) == 1 and started[0][-1] == "backup"

    monkeypatch.setenv("MAI_BACKUP", "off")
    off = controller.DailyBackup(dt.datetime(2026, 11, 12, 1, 0, tzinfo=utc))
    assert off.due(dt.datetime(2026, 11, 12, 2, 0, tzinfo=utc)) is False


class _Done:
    returncode = 0

    def poll(self):
        return 0


def test_daily_backup_waits_during_a_hold(monkeypatch):
    import datetime as dt

    utc = dt.UTC
    b = controller.DailyBackup(dt.datetime(2026, 11, 12, 1, 0, tzinfo=utc))
    started = []
    monkeypatch.setattr(
        controller.subprocess,
        "Popen",
        lambda argv, **kw: started.append(argv) or _Done(),
    )
    b.check(dt.datetime(2026, 11, 12, 2, 0, tzinfo=utc), held=True)
    assert started == []
    b.check(dt.datetime(2026, 11, 12, 2, 1, tzinfo=utc), held=False)
    assert len(started) == 1


def test_the_grants_run_in_a_thread(monkeypatch):
    import threading

    gate = threading.Event()
    monkeypatch.setattr(
        controller, "grant_mailbox_access", lambda timeout: gate.wait(5) or True
    )
    run = controller.GrantRun()
    run.start()
    assert run.running() and not run.done()
    gate.set()
    assert run._thread is not None
    run._thread.join(5)
    assert run.done() and run.take() is True and not run.running()


def test_a_stale_hold_is_not_removed_while_a_restore_holds_the_lock(monkeypatch):
    import contextlib

    released = []

    @contextlib.contextmanager
    def busy():
        raise controller.supervisor.HoldBusy("a restore runs")
        yield

    monkeypatch.setattr(controller.supervisor, "exclusive", busy)
    monkeypatch.setattr(controller.supervisor, "stale", lambda: True)
    monkeypatch.setattr(controller.supervisor, "release", lambda: released.append(True))
    controller.release_stale_hold()
    assert released == []


def _serve(status):
    import http.server
    import threading

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(status)
            self.end_headers()

        def log_message(self, format, *args):
            pass

    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f"http://127.0.0.1:{server.server_port}/healthz"


def test_health_prints_a_summary_when_it_passes(tmp_path, monkeypatch, capsys):
    import json

    monkeypatch.setattr(controller, "STATE_FILE", tmp_path / "state.json")
    names = ["app", "postfix", "milter", "dovecot", "postgres"]
    (tmp_path / "state.json").write_text(
        json.dumps({n: {"running": True} for n in names})
    )
    server, url = _serve(200)
    try:
        assert controller.health(url=url, timeout=2) == 0
    finally:
        server.shutdown()
    assert (
        capsys.readouterr().out.strip()
        == "mai: healthy (postgres, dovecot, milter, postfix, app)"
    )


def test_health_names_each_daemon_that_does_not_run(tmp_path, monkeypatch, capsys):
    import json

    monkeypatch.setattr(controller, "STATE_FILE", tmp_path / "state.json")
    state = {
        "app": {"running": True},
        "postfix": {"running": False},
        "dovecot": {"running": False},
    }
    (tmp_path / "state.json").write_text(json.dumps(state))
    assert controller.health(url="http://127.0.0.1:9/healthz", timeout=0.5) == 1
    assert (
        capsys.readouterr().out.strip()
        == "mai: not healthy: not running: dovecot, postfix"
    )


def test_health_reports_an_error_status_of_healthz(tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(controller, "STATE_FILE", tmp_path / "absent.json")
    server, url = _serve(503)
    try:
        assert controller.health(url=url, timeout=2) == 1
    finally:
        server.shutdown()
    out = capsys.readouterr().out.strip()
    assert out.startswith("mai: not healthy: /healthz")


def test_start_order_takes_a_generator():
    names = (n for n in ["app", "extra", "postgres"])
    assert controller._in_start_order(names) == ["postgres", "app", "extra"]
