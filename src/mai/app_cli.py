"""The mai command line.

Each command imports what it needs when it runs, thus a command works when
a different module is missing or broken. The commands that use a service
build the bustan application context, call the service, and close it.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import os
import sys
from pathlib import Path


def _call(token_path: str, method: str, *args, **kwargs):
    """Build the application context, call one async service method, close."""
    import importlib

    from bustan import create_app_context

    from mai.app_module import AppModule

    module_name, cls_name = token_path.rsplit(".", 1)
    token = getattr(importlib.import_module(module_name), cls_name)

    async def scenario():
        context = create_app_context(AppModule)
        await context.init()
        try:
            result = getattr(context.get(token), method)(*args, **kwargs)
            if asyncio.iscoroutine(result):
                result = await result
            return result
        finally:
            await context.close()

    return asyncio.run(scenario())


CRED = "mai.modules.credentials.CredentialService"
WATCH = "mai.modules.notices.WatchService"
OPS = "mai.modules.ops.OpsService"
HOOK_FORMATS = ("plain", "claude", "cursor", "copilot", "codex", "antigravity")


def _print_json(obj) -> None:
    print(json.dumps(obj, indent=2, default=str))


def cmd_run(args) -> int:
    from mai import app_run

    return int(app_run.run() or 0)


def cmd_health(args) -> int:
    from mai import app_run

    return int(app_run.health())


def cmd_init(args) -> int:
    from mai import app_migrate

    applied = app_migrate.migrate()
    result = app_migrate.init_accounts()
    access = app_migrate.grant_mailbox_access()
    print(f"migrations applied: {', '.join(map(str, applied)) or 'none'}")
    print(f"accounts created:   {', '.join(result.get('created', [])) or 'none'}")
    print(
        f"mailbox grants:     {len(access.get('granted', []))} given, {len(access.get('failed', []))} failed"
    )
    return 0


def cmd_agent_add(args) -> int:
    for name in args.names:
        res = _call(CRED, "agent_add", name)
        state = "added" if res.get("created") else "exists"
        print(f"{state}:{' ' * (11 - len(state))}{res['address']}")
        if not res.get("acl_set"):
            print("warning:    doveadm acl set did not run", file=sys.stderr)
    return 0


def cmd_agent_list(args) -> int:
    rows = _call(CRED, "agent_list")
    if args.json:
        _print_json(rows)
        return 0
    print(f"{'AGENT':40} {'STATUS':8} SESSIONS")
    for r in rows:
        print(f"{r['agent']:40} {r['status']:8} {r['sessions']}")
    return 0


def _show_credential(res: dict, file) -> None:
    print(f"login:      {res['address']}")
    if file:
        print(f"credential: written to {file}")
    else:
        print(f"credential: {res['credential']}")
    print(f"expires:    {res['expires_at'] or 'never'}")


def cmd_session_new(args) -> int:
    res = _call(
        CRED,
        "session_new",
        args.agent,
        args.label,
        expires_days=args.expires,
        file=args.file,
    )
    if args.credential_only:
        # Only the credential line: the host shim writes it to a file on the host (--save).
        print(res["credential"])
    else:
        _show_credential(res, args.file)
    if not res.get("acl_set"):
        print("warning:    doveadm acl set did not run", file=sys.stderr)
    return 0


def cmd_session_list(args) -> int:
    rows = _call(CRED, "session_list", agent=args.agent)
    if args.json:
        _print_json(rows)
        return 0
    print(f"{'LOGIN':40} {'STATUS':8} {'EXPIRES':21} LAST LOGIN")
    for r in rows:
        print(
            f"{r['login']:40} {r['status']:8} {r['expires_at'] or '-':21}"
            f" {r['last_login_at'] or '-'}"
        )
    return 0


def cmd_session_rotate(args) -> int:
    res = _call(CRED, "session_rotate", args.login, file=args.file)
    if args.credential_only:
        print(res["credential"])
    else:
        _show_credential(res, args.file)
    return 0


def cmd_session_revoke(args) -> int:
    res = _call(CRED, "session_revoke", args.login)
    print(f"revoked:    {res['login']}")
    return 0


def cmd_mcp(args) -> int:
    from mai import app_stdio

    return int(app_stdio.main() or 0)


def cmd_serve_http(args) -> int:
    from mai import app_main

    app_main.serve(host=args.host, port=args.port)
    return 0


def cmd_watch(args) -> int:
    return int(_call(WATCH, "watch", args.credential_file, args.timeout) or 0)


def cmd_hook(args) -> int:
    # A hook never fails the app that runs it.
    with contextlib.suppress(Exception):
        _call(WATCH, "hook", args.credential_file, fmt=args.format, event=args.event)
    return 0


def cmd_backup(args) -> int:
    res = _call(OPS, "backup")
    if res is not None:
        _print_json(res)
    return 0


def cmd_restore(args) -> int:
    res = _call(OPS, "restore", args.path)
    if res is not None:
        _print_json(res)
    return 0


def cmd_import_maildir(args) -> int:
    res = _call(
        OPS,
        "import_maildir",
        args.path,
        dry_run=args.dry_run,
        add_agents=args.add_agents,
        rename_header_prefix=args.rename_header_prefix,
    )
    if res is not None:
        _print_json(res)
    return 0


def cmd_milter(args) -> int:
    from mai import app_milter

    argv = ["--socket", args.socket or "/data/postfix/private/mai-milter"]
    return int(app_milter.main(argv) or 0)


def cmd_project_seed(args) -> int:
    """Make or update a board project, its supervisor, and one worker lane, with memberships."""
    import psycopg
    from psycopg.types.json import Jsonb

    from mai.platform.database import DEFAULT_DSN

    done_rule = {
        "checks": [
            {"name": "tests", "required": True, "min_coverage": args.min_coverage}
        ]
    }
    with psycopg.connect(DEFAULT_DSN) as conn:
        for agent in (args.supervisor, args.worker):
            conn.execute(
                "INSERT INTO mail_auth.principal (login, kind, status) VALUES (%s, 'agent', 'active')"
                " ON CONFLICT (login) DO NOTHING",
                (agent,),
            )
        conn.execute(
            "INSERT INTO board.project (name, goal, done_rule) VALUES (%s, %s, %s)"
            " ON CONFLICT (name) DO UPDATE SET done_rule = EXCLUDED.done_rule",
            (args.project, args.goal, Jsonb(done_rule)),
        )
        conn.execute(
            "INSERT INTO board.lane (project, name, agent) VALUES (%s, %s, %s)"
            " ON CONFLICT (project, name) DO UPDATE SET agent = EXCLUDED.agent",
            (args.project, args.lane or args.worker, args.worker),
        )
        for agent, role in ((args.supervisor, "supervisor"), (args.worker, "worker")):
            conn.execute(
                "INSERT INTO mail_auth.membership (project, agent, role) VALUES (%s, %s, %s)"
                " ON CONFLICT (project, agent) DO UPDATE SET role = EXCLUDED.role",
                (args.project, agent, role),
            )
    print(
        f"project {args.project}: supervisor {args.supervisor},"
        f" lane {args.lane or args.worker} ({args.worker})"
    )
    return 0


def _strip_domain(login: str) -> str:
    return login.split("@", 1)[0]


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="mai", description="mai 2.0")
    sub = p.add_subparsers(dest="command", required=True)

    sub.add_parser("run", help="run the container main").set_defaults(func=cmd_run)
    sub.add_parser("health", help="check the children").set_defaults(func=cmd_health)
    sub.add_parser("init", help="apply migrations and make accounts").set_defaults(
        func=cmd_init
    )

    a = sub.add_parser("agent", help="manage agent accounts")
    asub = a.add_subparsers(dest="agent_command", required=True)
    aa = asub.add_parser("add", help="make one or more agent accounts")
    aa.add_argument("names", nargs="+", metavar="NAME")
    aa.set_defaults(func=cmd_agent_add)
    al = asub.add_parser("list", help="list the agent accounts")
    al.add_argument("--json", action="store_true")
    al.set_defaults(func=cmd_agent_list)

    s = sub.add_parser("session", help="manage session credentials")
    ss = s.add_subparsers(dest="session_command", required=True)
    n = ss.add_parser("new", help="make a session credential")
    n.add_argument("agent")
    n.add_argument("--label", required=True)
    n.add_argument("--expires", default="7d", help="for example 7d or 12h")
    n.add_argument("--file", help="write the credential to this file (mode 0600)")
    n.add_argument(
        "--credential-only",
        action="store_true",
        help="print only the credential line (the host shim uses it for --save)",
    )
    n.set_defaults(func=cmd_session_new)
    ls = ss.add_parser("list", help="list session logins")
    ls.add_argument("--agent")
    ls.add_argument("--json", action="store_true")
    ls.set_defaults(func=cmd_session_list)
    r = ss.add_parser("rotate", help="give a session a new secret")
    r.add_argument("login", type=_strip_domain)
    r.add_argument("--file")
    r.add_argument(
        "--credential-only",
        action="store_true",
        help="print only the credential line (the host shim uses it for --save)",
    )
    r.set_defaults(func=cmd_session_rotate)
    v = ss.add_parser("revoke", help="revoke a session")
    v.add_argument("login", type=_strip_domain)
    v.set_defaults(func=cmd_session_revoke)

    pj = sub.add_parser("project", help="manage board projects")
    pjs = pj.add_subparsers(dest="project_cmd", required=True)
    sd = pjs.add_parser(
        "seed", help="make a project with a supervisor and a worker lane"
    )
    sd.add_argument("project")
    sd.add_argument("--goal", default="seeded project")
    sd.add_argument("--supervisor", required=True, help="the supervisor agent")
    sd.add_argument("--worker", required=True, help="the worker agent of the lane")
    sd.add_argument("--lane", help="the lane name (default: the worker name)")
    sd.add_argument("--min-coverage", type=float, default=80)
    sd.set_defaults(func=cmd_project_seed)
    sub.add_parser("mcp", help="serve MCP on stdio").set_defaults(func=cmd_mcp)
    h = sub.add_parser("serve-http", help="serve MCP and the UI on HTTP")
    h.add_argument("--host", default="0.0.0.0")
    h.add_argument("--port", type=int, default=8080)
    h.set_defaults(func=cmd_serve_http)

    w = sub.add_parser("watch", help="wait for new mail")
    w.add_argument("--credential-file", required=True)
    w.add_argument("--timeout", type=int, default=1500, help="seconds")
    w.set_defaults(func=cmd_watch)
    k = sub.add_parser("hook", help="show notices of new mail once")
    k.add_argument(
        "--credential-file",
        required=True,
        help="the credential file, or - to read the credential from the first line of stdin",
    )
    k.add_argument(
        "--format",
        choices=HOOK_FORMATS,
        default="plain",
        help="the output format of the app that runs the hook",
    )
    k.add_argument("--event", help="the hook event name of the app, for example Stop")
    k.set_defaults(func=cmd_hook)

    sub.add_parser("backup", help="back up the database and mail").set_defaults(
        func=cmd_backup
    )
    rs = sub.add_parser("restore", help="restore a backup")
    rs.add_argument("path")
    rs.set_defaults(func=cmd_restore)
    im = sub.add_parser("import-maildir", help="import a 0.5.0 maildir")
    im.add_argument("path")
    im.add_argument(
        "--add-agents",
        action="store_true",
        help="make the agent account of each folder that has no account",
    )
    im.add_argument(
        "--rename-header-prefix",
        action="append",
        metavar="OLD=NEW",
        help="change the header names that start with OLD to start with NEW (repeatable)",
    )
    im.add_argument(
        "--dry-run", action="store_true", help="count the mails and write nothing"
    )
    im.set_defaults(func=cmd_import_maildir)
    sh = sub.add_parser("shim", help="the host shims and compose file of the image")
    shs = sh.add_subparsers(dest="shim_command", required=True)
    si = shs.add_parser("install", help="write the shims and compose.yaml to a folder")
    si.add_argument(
        "dir", help="a folder in the container, for example a bind mount /out"
    )
    si.add_argument(
        "--force", action="store_true", help="also write compose.yaml again"
    )
    si.set_defaults(func=cmd_shim_install)

    m = sub.add_parser("milter", help="run the Postfix milter")
    m.add_argument("--socket", help="the Unix socket path")
    m.set_defaults(func=cmd_milter)
    return p


SHIM_FILES = ("mai", "mai.ps1", "mai.cmd")


def cmd_shim_install(args) -> int:
    """Write the host shims and the compose file of the image to a host folder.

    The folder is a bind mount, for example -v "$HOME/.mai/bin:/out". The shims
    are always written again, thus the command also updates them. The compose
    file is written only if it does not exist, or with --force, because the
    user can change it.
    """
    share = Path(os.environ.get("MAI_SHARE", "/opt/mai/share"))
    target = Path(args.dir)
    target.mkdir(parents=True, exist_ok=True)
    for name in SHIM_FILES:
        (target / name).write_bytes((share / name).read_bytes())
        (target / name).chmod(0o755 if name == "mai" else 0o644)
        print(f"wrote:   {target / name}")
    compose = target / "compose.yaml"
    if compose.exists() and not args.force:
        print(f"kept:    {compose} (use --force to write it again)")
    else:
        compose.write_bytes((share / "compose.yaml").read_bytes())
        print(f"wrote:   {compose}")
    return 0


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except KeyboardInterrupt:
        return 130
    except ValueError as e:
        print(f"mai: {e}", file=sys.stderr)
        return 1
    except Exception as e:  # noqa: BLE001 - the last guard: show a short error, never a secret.
        code = getattr(e, "code", None)
        msg = code if isinstance(code, str) else f"{type(e).__name__}: {e}"
        print(f"mai: {msg}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
