"""The container main: PostgreSQL, Dovecot, Postfix, the milter and the app.

The controller runs as root under tini. It prepares /data, starts
PostgreSQL on its Unix socket only, applies the migrations, renders the
daemon configuration, and then supervises the children. A child that
stops is started again with a delay that doubles up to 60 seconds. At
SIGTERM the children stop in this order: app, Postfix, milter, Dovecot,
PostgreSQL. The app child serves the bustan application with uvicorn.
"""

from __future__ import annotations

import datetime
import json
import logging
import os
import shutil
import signal
import subprocess
import sys
import threading
import time
import urllib.request
from pathlib import Path

from .platform import supervisor

log = logging.getLogger("mai.run")

DATA = Path(os.environ.get("MAI_DATA", "/data"))
ETC = Path(os.environ.get("MAI_ETC", "/opt/mai/etc"))
PGDATA = DATA / "pg"
PGSOCK = Path("/run/postgresql")
STATE_FILE = Path("/run/mai/state.json")
POSTFIX_DIST = Path("/opt/postfix-dist")
DOVECOT_DIST = Path("/opt/dovecot-dist")
MAX_BACKOFF = 60.0
STOP_ORDER = ("app", "postfix", "milter", "dovecot", "postgres")
START_ORDER = ("postgres", "dovecot", "milter", "postfix", "app")
MILTER_SOCKET = Path(
    os.environ.get("MILTER_SOCKET", "/data/postfix/private/mai-milter")
)

PG_HBA = """\
# Unix socket only. Each OS user maps to its own role; root may act
# as any of the service roles for setup and checks.
local all all peer map=mai
"""

PG_IDENT = """\
mai postgres postgres
mai root postgres
mai root mai
mai root dovecot
mai root postfix
mai mai mai
mai dovecot dovecot
mai postfix postfix
"""


def _run(argv, **kw):
    return subprocess.run(argv, check=True, **kw)


def _chown(
    path: Path, user: str, group: str | None = None, mode: int | None = None
) -> None:
    shutil.chown(path, user, group or user)
    if mode is not None:
        os.chmod(path, mode)


# Preparation


def make_dirs() -> None:
    for name in (
        "pg",
        "mail",
        "postfix",
        "postfix-data",
        "tls",
        "backup",
        "secrets",
        "dovecot",
    ):
        (DATA / name).mkdir(parents=True, exist_ok=True)
    _chown(DATA / "pg", "postgres", "postgres", 0o700)
    _chown(DATA / "mail", "vmail", "vmail", 0o750)
    _chown(DATA / "postfix", "root", "root", 0o755)
    # Dovecot binds its LMTP and auth sockets in the Postfix queue, so the
    # private directory must exist before Postfix first runs.
    (DATA / "postfix" / "private").mkdir(exist_ok=True)
    _chown(DATA / "postfix" / "private", "postfix", "root", 0o700)
    _chown(DATA / "postfix-data", "postfix", "postfix", 0o700)
    _chown(DATA / "dovecot", "vmail", "vmail", 0o750)
    _chown(DATA / "secrets", "mai", "mai", 0o700)
    _chown(DATA / "tls", "root", "root", 0o755)
    PGSOCK.mkdir(parents=True, exist_ok=True)
    _chown(PGSOCK, "postgres", "postgres", 0o775)
    STATE_FILE.parent.mkdir(parents=True, exist_ok=True)


def render_configs() -> None:
    """Write /etc/postfix and /etc/dovecot from the templates."""
    for dist, dest in (
        (POSTFIX_DIST, Path("/etc/postfix")),
        (DOVECOT_DIST, Path("/etc/dovecot")),
    ):
        dest.mkdir(parents=True, exist_ok=True)
        # On a read-only root, /etc/postfix is a tmpfs: put the package
        # files (dynamic map list, postfix-files) back first.
        if dist.is_dir():
            shutil.copytree(dist, dest, dirs_exist_ok=True, symlinks=True)
    for src in (ETC / "postfix").glob("*"):
        dst = Path("/etc/postfix") / src.name
        shutil.copyfile(src, dst)
        if src.name.startswith("pgsql-"):
            _chown(dst, "root", "postfix", 0o640)
        else:
            os.chmod(dst, 0o644)
    conf = Path("/etc/dovecot/dovecot.conf")
    shutil.copyfile(ETC / "dovecot" / "dovecot.conf", conf)
    os.chmod(conf, 0o644)
    # Package snippets of 2.3 style would break the 2.4 parser.
    confd = Path("/etc/dovecot/conf.d")
    if confd.is_dir():
        shutil.rmtree(confd)


def init_postgres() -> None:
    if (PGDATA / "PG_VERSION").exists():
        return
    log.info("initdb in %s", PGDATA)
    _run(
        [
            "gosu",
            "postgres",
            "initdb",
            "-D",
            str(PGDATA),
            "-U",
            "postgres",
            "--auth-local=peer",
            "--auth-host=reject",
            "--encoding=UTF8",
            "--locale=C",
        ],
        stdout=subprocess.DEVNULL,
    )
    (PGDATA / "pg_hba.conf").write_text(PG_HBA)
    (PGDATA / "pg_ident.conf").write_text(PG_IDENT)
    for f in ("pg_hba.conf", "pg_ident.conf"):
        _chown(PGDATA / f, "postgres", "postgres", 0o600)


def postgres_argv() -> list[str]:
    return [
        "gosu",
        "postgres",
        "postgres",
        "-D",
        str(PGDATA),
        "-c",
        "listen_addresses=",
        "-c",
        f"unix_socket_directories={PGSOCK}",
    ]


def wait_postgres(timeout: float = 60.0) -> None:
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        r = subprocess.run(
            ["pg_isready", "-q", "-h", str(PGSOCK), "-U", "postgres"], check=False
        )
        if r.returncode == 0:
            return
        time.sleep(0.5)
    raise RuntimeError("PostgreSQL did not become ready")


def wait_dovecot(timeout: float = 30.0) -> bool:
    """Wait until Dovecot answers a userdb lookup. Give False after the timeout."""
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        r = subprocess.run(
            ["doveadm", "user", "board"],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if r.returncode == 0:
            return True
        time.sleep(0.5)
    return False


def grant_mailbox_access(timeout: float = 30.0) -> bool:
    """Give the board and each live session its rights on the agent queues.

    Dovecot keeps the ACL, thus the grants run after Dovecot starts. Without
    the board grant the router sees an empty queue. Give True only when each
    grant succeeded: the controller tries again after GRANT_RETRY otherwise.
    """
    if not wait_dovecot(timeout):
        log.error("Dovecot did not answer: the mailbox grants did not run")
        return False
    try:
        from mai import app_migrate

        result = app_migrate.grant_mailbox_access()
    except Exception as e:  # noqa: BLE001 - the daemons still run
        log.error("mailbox grants failed: %s", type(e).__name__)
        return False
    failed = len(result.get("failed", []))
    log.info(
        "mailbox grants: %d given, %d failed", len(result.get("granted", [])), failed
    )
    return failed == 0


# The time before the controller tries the mailbox grants again after a failure.
GRANT_RETRY = 60.0


class GrantRun:
    """One run of grant_mailbox_access in a thread, for the supervision loop."""

    def __init__(self) -> None:
        self._thread: threading.Thread | None = None
        self._result: bool | None = None

    def start(self) -> None:
        self._result = None

        def work() -> None:
            self._result = grant_mailbox_access(timeout=5.0)

        self._thread = threading.Thread(target=work, name="mai-grants", daemon=True)
        self._thread.start()

    def running(self) -> bool:
        return self._thread is not None and self._thread.is_alive()

    def done(self) -> bool:
        return self._thread is not None and not self._thread.is_alive()

    def take(self) -> bool:
        self._thread = None
        return bool(self._result)


def release_stale_hold() -> None:
    """Remove a hold whose command stopped. The restore lock comes first.

    A restore holds the lock from before its hold until after its release.
    Thus with the lock, no live restore can write a new hold while the
    controller removes the stale one.
    """
    try:
        with supervisor.exclusive():
            if supervisor.stale():
                log.warning(
                    "the hold of a stopped command is not valid: the daemons start again"
                )
                supervisor.release()
    except supervisor.HoldBusy:
        return


BACKUP_HOUR_UTC = 2


class DailyBackup:
    """Runs "mai backup" as root once a day at BACKUP_HOUR_UTC.

    The controller runs the backup, not the app: the app user cannot read
    the mail files of vmail or write /data/backup. The backup keeps the
    newest 14. MAI_BACKUP=off turns it off. A start after the hour of
    the day waits for the next day, thus a restart makes no extra backup.
    """

    def __init__(self, now: datetime.datetime) -> None:
        self.enabled = os.environ.get("MAI_BACKUP", "on") != "off"
        self.last_day = now.date() if now.hour >= BACKUP_HOUR_UTC else None
        self.proc: subprocess.Popen | None = None

    def due(self, now: datetime.datetime) -> bool:
        return (
            self.enabled
            and now.hour >= BACKUP_HOUR_UTC
            and self.last_day != now.date()
            and (self.proc is None or self.proc.poll() is not None)
        )

    def check(self, now: datetime.datetime, held: bool = False) -> None:
        if self.proc is not None and self.proc.poll() is not None:
            if self.proc.returncode != 0:
                log.error("the daily backup stopped with %s", self.proc.returncode)
            self.proc = None
        if self.due(now) and not held:
            # During a hold (a restore) the backup waits: it starts after the hold.
            self.last_day = now.date()
            log.info("start the daily backup")
            self.proc = subprocess.Popen(
                [str(Path(sys.executable).with_name("mai")), "backup"],
                stdout=subprocess.DEVNULL,
            )


def psql(sql: str, db: str = "postgres") -> str:
    r = _run(
        [
            "psql",
            "-X",
            "-q",
            "-At",
            "-v",
            "ON_ERROR_STOP=1",
            "-h",
            str(PGSOCK),
            "-U",
            "postgres",
            "-d",
            db,
            "-c",
            sql,
        ],
        capture_output=True,
        text=True,
    )
    return r.stdout


BOOTSTRAP_ROLES = """
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'mai') THEN CREATE ROLE mai LOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dovecot') THEN CREATE ROLE dovecot LOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'postfix') THEN CREATE ROLE postfix LOGIN; END IF;
END $$;
"""

DAEMON_GRANTS = """
GRANT USAGE ON SCHEMA mail_auth TO dovecot, postfix;
GRANT SELECT ON mail_auth.principal TO dovecot;
DO $$
DECLARE r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['sender_login','mailbox_address','alias'] LOOP
    IF to_regclass('mail_auth.' || r) IS NOT NULL THEN
      EXECUTE format('GRANT SELECT ON mail_auth.%I TO postfix', r);
    END IF;
  END LOOP;
END $$;
"""


def bootstrap_db() -> None:
    psql(BOOTSTRAP_ROLES)
    if psql("SELECT 1 FROM pg_database WHERE datname = 'mai'").strip() != "1":
        psql("CREATE DATABASE mai OWNER mai")
    psql(
        "REVOKE ALL ON DATABASE mai FROM PUBLIC; "
        "GRANT CONNECT ON DATABASE mai TO mai, dovecot, postfix"
    )
    # The app needs to create its schemas in its own database.
    psql("GRANT CREATE ON DATABASE mai TO mai")


def migrate_and_seed() -> None:
    try:
        from mai import app_migrate

        applied = app_migrate.migrate()
        log.info("migrations applied: %s", applied or "none")
    except Exception as e:  # noqa: BLE001 - the controller still supervises the daemons
        log.error(
            "migrations failed, the app cannot serve: %s: %s", type(e).__name__, e
        )
        return
    try:
        psql(DAEMON_GRANTS, db="mai")
    except subprocess.CalledProcessError as e:
        log.error("daemon grants failed: %s", (e.stderr or "").strip())
    try:
        result = app_migrate.init_accounts()
        log.info("accounts created: %s", ", ".join(result.get("created", [])) or "none")
    except Exception as e:  # noqa: BLE001 - the daemons still start
        log.error("account setup failed: %s", type(e).__name__)
    for f in (DATA / "secrets").glob("*.cred"):
        _chown(f, "mai", "mai", 0o600)


def ensure_cert(directory: Path, days: int = 3650) -> tuple[Path, Path]:
    """Make a self-signed cert.pem and key.pem for localhost once; keep them after that."""
    directory.mkdir(parents=True, exist_ok=True)
    cert, key = directory / "cert.pem", directory / "key.pem"
    if cert.exists() and key.exists():
        return cert, key
    _run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:3072",
            "-nodes",
            "-sha256",
            "-days",
            str(days),
            "-subj",
            "/CN=localhost",
            "-addext",
            "subjectAltName=DNS:localhost,DNS:mail.mai.internal,IP:127.0.0.1",
            "-keyout",
            str(key),
            "-out",
            str(cert),
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    os.chmod(key, 0o600)
    os.chmod(cert, 0o644)
    log.info("made a self-signed certificate in %s", directory)
    return cert, key


def ensure_tls() -> None:
    ensure_cert(DATA / "tls")


def prepare_milter_socket() -> None:
    """Remove a stale milter socket. The milter runs as postfix and binds it again."""
    MILTER_SOCKET.parent.mkdir(parents=True, exist_ok=True)
    try:
        MILTER_SOCKET.unlink()
    except FileNotFoundError:
        pass


# Supervision


class Child:
    def __init__(
        self,
        name: str,
        argv: list[str],
        stop_sig: int = signal.SIGTERM,
        stop_argv: list[str] | None = None,
    ):
        self.name = name
        self.argv = argv
        self.stop_sig = stop_sig
        self.stop_argv = stop_argv
        self.proc: subprocess.Popen | None = None
        self.backoff = 1.0
        self.next_start = 0.0
        self.started_at = 0.0
        self.restarts = 0

    def start(self) -> None:
        log.info("start %s", self.name)
        self.proc = subprocess.Popen(self.argv)
        self.started_at = time.monotonic()

    def alive(self) -> bool:
        return self.proc is not None and self.proc.poll() is None

    def check_held(self) -> None:
        """A held daemon is not started again. Its next start, after the hold, has no delay."""
        if not self.alive() and self.proc is not None:
            log.info("%s stopped while held; it starts after the hold", self.name)
            self.proc = None
            self.next_start = 0.0
            self.backoff = 1.0

    def check(self, now: float) -> None:
        if self.alive():
            if now - self.started_at > 30:
                self.backoff = 1.0
            return
        if self.proc is not None:
            log.warning(
                "%s stopped with %s; start again in %.0fs",
                self.name,
                self.proc.returncode,
                self.backoff,
            )
            self.next_start = now + self.backoff
            self.backoff = min(self.backoff * 2, MAX_BACKOFF)
            self.proc = None
            self.restarts += 1
        if now >= self.next_start:
            self.start()

    def stop(self, timeout: float = 20.0) -> None:
        proc = self.proc
        if proc is None or not self.alive():
            return
        log.info("stop %s", self.name)
        if self.stop_argv:
            subprocess.run(
                self.stop_argv,
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        else:
            proc.send_signal(self.stop_sig)
        try:
            proc.wait(timeout)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()

    def state(self) -> dict:
        return {
            "running": self.alive(),
            "pid": self.proc.pid if self.proc is not None and self.alive() else None,
            "restarts": self.restarts,
        }


def app_argv() -> list[str]:
    return [
        "gosu",
        "mai",
        sys.executable,
        "-m",
        "mai.app_main",
        "--host",
        "0.0.0.0",
        "--port",
        os.environ.get("HTTP_PORT", "8080"),
    ]


def milter_argv() -> list[str]:
    return [
        "gosu",
        "postfix",
        sys.executable,
        "-m",
        "mai.app_milter",
        "--socket",
        str(MILTER_SOCKET),
    ]


def children() -> dict[str, Child]:
    return {
        "postgres": Child("postgres", postgres_argv(), stop_sig=signal.SIGINT),
        "dovecot": Child("dovecot", ["dovecot", "-F"], stop_argv=["doveadm", "stop"]),
        "milter": Child("milter", milter_argv()),
        "postfix": Child(
            "postfix", ["postfix", "start-fg"], stop_argv=["postfix", "stop"]
        ),
        "app": Child("app", app_argv()),
    }


def write_state(kids: dict[str, Child]) -> None:
    try:
        tmp = STATE_FILE.with_suffix(".tmp")
        tmp.write_text(json.dumps({n: c.state() for n, c in kids.items()}))
        tmp.replace(STATE_FILE)
    except OSError:
        pass


def child_states() -> dict:
    """The state of each child, as the controller last wrote it."""
    try:
        return json.loads(STATE_FILE.read_text())
    except (OSError, ValueError):
        return {}


def run() -> int:
    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s"
    )
    stop = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())

    make_dirs()
    supervisor.mark_controller()
    init_postgres()
    kids = children()
    kids["postgres"].start()
    wait_postgres()
    bootstrap_db()
    migrate_and_seed()
    ensure_tls()
    render_configs()
    prepare_milter_socket()
    grants_ok = False
    for name in START_ORDER[1:]:
        kids[name].start()
        if name == "dovecot":
            grants_ok = grant_mailbox_access()
    next_grant = time.monotonic() + GRANT_RETRY
    dovecot_started = kids["dovecot"].started_at
    backup = DailyBackup(datetime.datetime.now(datetime.UTC))
    grants = GrantRun()

    was_held = False
    while not stop.is_set():
        now = time.monotonic()
        if supervisor.stale():
            release_stale_hold()
        on_hold = supervisor.held()
        if was_held and not on_hold:
            # A restore can give a database of an earlier schema: migrate before the app starts.
            migrate_and_seed()
        was_held = bool(on_hold)
        for name in STOP_ORDER:
            if name in on_hold and kids[name].alive():
                kids[name].stop()
        for name in START_ORDER:
            if name in on_hold:
                kids[name].check_held()
            else:
                kids[name].check(now)
        if kids["dovecot"].started_at != dovecot_started and kids["dovecot"].alive():
            # A new Dovecot process: give the grants again, as at the first start.
            dovecot_started = kids["dovecot"].started_at
            grants_ok, next_grant = False, now
        if (
            not grants_ok
            and now >= next_grant
            and "dovecot" not in on_hold
            and not grants.running()
        ):
            # The grants run in a thread: the loop goes on and acts on SIGTERM at once.
            grants.start()
            next_grant = now + GRANT_RETRY
        if grants.done():
            grants_ok = grants.take()
        backup.check(datetime.datetime.now(datetime.UTC), held=bool(on_hold))
        write_state(kids)
        stop.wait(1.0)

    log.info("SIGTERM: stopping")
    for name in STOP_ORDER:
        kids[name].stop()
    write_state(kids)
    return 0


def _in_start_order(names) -> list[str]:
    names = set(names)  # a generator would be used up by the first "in"
    known = [n for n in START_ORDER if n in names]
    return known + sorted(n for n in names if n not in START_ORDER)


def health(url: str = "http://127.0.0.1:8080/healthz", timeout: float = 5.0) -> int:
    """0 when every child runs and the app answers /healthz, else 1.

    It prints one line in each case, thus a person sees the result. The Docker
    health check reads only the exit code.
    """
    states = child_states()
    down = _in_start_order(n for n, s in states.items() if not s.get("running"))
    if down:
        print(f"mai: not healthy: not running: {', '.join(down)}")
        return 1
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            if r.status != 200:
                print(f"mai: not healthy: /healthz gave HTTP {r.status}")
                return 1
    except Exception as e:  # noqa: BLE001 - any failure means unhealthy
        print(f"mai: not healthy: /healthz failed: {e}")
        return 1
    running = _in_start_order(states)
    print(f"mai: healthy ({', '.join(running)})" if running else "mai: healthy")
    return 0


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "health":
        sys.exit(health())
    sys.exit(run())
