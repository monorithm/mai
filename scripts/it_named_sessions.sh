#!/bin/sh
# End-to-end test of the named sessions: the host CLI, the bridge, and the hook
# against a mai image.
# Usage: scripts/it_named_sessions.sh [IMAGE]   (default mai:wf-ns-e2e)
# MAI_IT_NAME sets the instance (default mai-wf-ns-e2e), and MAI_IT_PORT sets
# the host port (default 18990).
#
# The script builds mai with the tag presencefake into a scratch folder. Thus
# the presence check always agrees and shows no prompt. MAI_HOME is a scratch
# folder, and the keys go to the keychain service dev.mai.test.e2e, not to the
# service of the user. The script refuses the instance "mai" and the port 8765.
# It never pulls an image and never prints a key. At the end, mai uninstall
# removes the instance, and the script does a check that no object stays.
# It needs Go, Python 3, a container engine, and a keychain: macOS, or Linux
# with a Secret Service.
set -eu
image=${1:-mai:wf-ns-e2e}
instance=${MAI_IT_NAME:-mai-wf-ns-e2e}
port=${MAI_IT_PORT:-18990}
root=$(cd "$(dirname "$0")/.." && pwd)

fail() {
  echo "FAIL: $1" >&2
  exit 1
}
[ "$instance" != mai ] || fail "the instance mai is the install of the user"
[ "$port" != 8765 ] || fail "the port 8765 is the port of the user"
docker image inspect "$image" >/dev/null 2>&1 || fail "the image $image is not local"

work=$(mktemp -d "${TMPDIR:-/tmp}/mai-it-ns.XXXXXX")
mai="$work/bin/mai"
export MAI_HOME="$work/home"
export MAI_TEST_KEYCHAIN_SERVICE=dev.mai.test.e2e
unset MAI_TEST_PRESENCE

cleanup() {
  code=$?
  if [ -x "$mai" ]; then
    "$mai" uninstall --instance "$instance" --yes >/dev/null 2>&1 || true
  fi
  docker rm -f "$instance" >/dev/null 2>&1 || true
  docker volume rm "${instance}3-data" "${instance}3-spool" >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$code"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

if docker container inspect "$instance" >/dev/null 2>&1; then
  fail "a container $instance exists. Remove it first"
fi

(cd "$root/cli" && go build -tags presencefake -o "$mai" ./cmd/mai) || fail "go build"
echo "ok: build mai with the tag presencefake"

"$mai" start --instance "$instance" --port "$port" --image "$image" --no-pull >"$work/start.log" 2>&1 ||
  { tail -20 "$work/start.log" >&2; fail "mai start"; }
echo "ok: mai start --instance $instance --port $port --image $image --no-pull"

[ "$("$mai" agent add cursor)" = cursor ] || fail "mai agent add cursor"
echo "ok: mai agent add cursor"

for label in lane-a lane-b; do
  out=$("$mai" session new cursor --label "$label") || fail "mai session new cursor --label $label"
  [ "$out" = "cursor.$label" ] || fail "mai session new cursor --label $label printed $out"
  echo "ok: mai session new cursor --label $label prints only cursor.$label"
done
if "$mai" session new cursor --label lane-a >/dev/null 2>"$work/dup.log"; then
  fail "a second mai session new cursor --label lane-a did not fail"
fi
grep -q SESSION_EXISTS "$work/dup.log" || fail "a second session new gave no SESSION_EXISTS"
echo "ok: a second mai session new cursor --label lane-a gives SESSION_EXISTS"

cat >"$work/driver.py" <<'PY'
"""Drive one or two bridges "mai mcp --agent cursor" with JSON-RPC lines."""

import json
import os
import queue
import re
import secrets
import subprocess
import sys
import threading
import time

MAI, WORK = sys.argv[1], sys.argv[2]
HANDLE = re.compile(r"^ses-[a-z2-7]{16}$")
POOL = re.compile(r"^cursor\.s[0-9]+$")


class Fail(Exception):
    pass


def ok(text):
    print("ok: " + text, flush=True)


def check(cond, text):
    if not cond:
        raise Fail(text)


class Bridge:
    """One bridge process. The driver is its app, thus the parent of the bridge."""

    def __init__(self, name):
        self.name = name
        self.folder = os.path.join(WORK, "chat-" + name)
        os.makedirs(self.folder, exist_ok=True)
        self.log_path = os.path.join(WORK, "bridge-" + name + ".log")
        self.log = open(self.log_path, "wb")
        self.proc = subprocess.Popen(
            [MAI, "mcp", "--agent", "cursor"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=self.log,
            cwd=self.folder,
        )
        self.lines = queue.Queue()
        self.last = 0
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        for raw in self.proc.stdout:
            self.lines.put(raw)
        self.lines.put(None)

    def request(self, method, params=None, timeout=60):
        self.last += 1
        rid = self.last
        msg = {"jsonrpc": "2.0", "id": rid, "method": method}
        if params is not None:
            msg["params"] = params
        self.proc.stdin.write((json.dumps(msg) + "\n").encode())
        self.proc.stdin.flush()
        end = time.monotonic() + timeout
        while True:
            left = end - time.monotonic()
            check(left > 0, f"{self.name}: no answer to {method} in {timeout} s")
            try:
                raw = self.lines.get(timeout=left)
            except queue.Empty:
                continue
            check(raw is not None, f"{self.name}: the bridge stopped before the answer to {method}")
            m = json.loads(raw)
            if m.get("id") == rid:
                err = m.get("error")
                check(err is None, f"{self.name}: {method} gave the error {err}")
                return m["result"]
            check("error" not in m, f"{self.name}: an error with no request: {m['error']}")

    def notify(self, method):
        self.proc.stdin.write((json.dumps({"jsonrpc": "2.0", "method": method}) + "\n").encode())
        self.proc.stdin.flush()

    def call(self, tool, args=None, session=None, timeout=60):
        a = dict(args or {})
        if session is not None:
            a["session"] = session
        return self.request("tools/call", {"name": tool, "arguments": a}, timeout)

    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                return self.proc.wait(timeout=30)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
                return None
        return self.proc.returncode

    def tail(self):
        self.log.flush()
        with open(self.log_path, "rb") as f:
            return b"".join(f.readlines()[-20:]).decode(errors="replace")


def result(r, what):
    """The structured content of a tool result that is not an error."""
    if r.get("isError"):
        code = (r.get("structuredContent") or {}).get("error", {}).get("code")
        raise Fail(f"{what} gave the tool error {code}")
    return r["structuredContent"]


def error_code(r, what):
    check(r.get("isError") is True, f"{what} did not give isError true")
    return r["structuredContent"]["error"]["code"]


def same_text(r, what):
    check(json.loads(r["content"][0]["text"]) == r["structuredContent"], f"{what}: the text is not the JSON of structuredContent")


def mai(*args):
    p = subprocess.run([MAI, *args], capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=60)
    check(p.returncode == 0, f"mai {' '.join(args)} gave exit {p.returncode}: {p.stderr.strip()}")
    return p.stdout


def sessions():
    return {s["login"]: s for s in json.loads(mai("session", "list", "--json"))}


def main():
    one = Bridge("one")
    two = None
    try:
        run(one)
        two = Bridge("two")
        run_second(one, two)
        run_end(one)
    except Exception:
        for b in (one, two):
            if b is not None:
                print(f"--- bridge {b.name} log ---\n{b.tail()}", file=sys.stderr)
        raise
    finally:
        for b in (one, two):
            if b is not None:
                b.close()


def run(one):
    init = one.request("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "it", "version": "0"}})
    check(init["serverInfo"]["version"] == "3.1.0", f"the contract is {init['serverInfo']}")
    check("call login with the label of your session" in init["instructions"], "the instructions do not tell about login")
    one.notify("notifications/initialized")
    ok("initialize gives the contract 3.1.0, and the instructions tell about login")

    guide = one.request("resources/read", {"uri": "mai://guide"})
    check("login" in guide["contents"][0]["text"], "the guide does not tell about login")
    ok("resources/read mai://guide tells about login")

    tools = {t["name"]: t for t in one.request("tools/list")["tools"]}
    check({"login", "logout"} <= set(tools), "tools/list has no login or logout")
    check(tools["login"]["inputSchema"]["properties"]["session"]["type"] == "string", "login has no session string")
    check(tools["login"]["inputSchema"].get("required") == ["session"], "login does not require session")
    check(tools["logout"]["inputSchema"]["properties"]["session"]["type"] == "string", "logout has no session string")
    for name, t in tools.items():
        if name in ("login", "logout"):
            continue
        prop = t["inputSchema"]["properties"].get("session")
        check(prop is not None and prop.get("type") == "string" and prop.get("pattern") == "^ses-[a-z2-7]{16}$", f"{name} has no session property")
        check("session" not in t["inputSchema"].get("required", []), f"{name} requires session")
    ok(f"tools/list has login, logout, and the optional session property in the other {len(tools) - 2} tools")

    r = one.call("login", {"session": "lane-a"})
    a = result(r, "login lane-a")
    same_text(r, "login lane-a")
    check(HANDLE.match(a["session"]) and a["login"] == "cursor.lane-a" and a["agent"] == "cursor", f"login lane-a gave {a}")
    ok("login lane-a gives a handle A and the login cursor.lane-a")
    b = result(one.call("login", {"session": "lane-b"}), "login lane-b")
    check(HANDLE.match(b["session"]) and b["login"] == "cursor.lane-b", f"login lane-b gave {b}")
    check(b["session"] != a["session"], "the handles A and B are the same")
    ok("login lane-b gives a different handle B and the login cursor.lane-b")
    again = result(one.call("login", {"session": "cursor.lane-a"}), "login cursor.lane-a")
    check(again["session"] == a["session"], "a second login to lane-a gave a different handle")
    ok("a second login with cursor.lane-a gives the same handle A")
    one.ha, one.hb = a["session"], b["session"]

    w = result(one.call("whoami", session=one.ha), "whoami A")
    check(w["login"] == "cursor.lane-a", f"whoami A is {w['login']}")
    ok("whoami with session A gives cursor.lane-a")
    w = result(one.call("whoami", session=one.hb), "whoami B")
    check(w["login"] == "cursor.lane-b", f"whoami B is {w['login']}")
    ok("whoami with session B gives cursor.lane-b")
    w = result(one.call("whoami"), "whoami")
    check(POOL.match(w["login"] or ""), f"whoami with no session is {w['login']}")
    one.pool = w["login"]
    ok(f"whoami with no session gives the pool session {w['login']}")

    subject = "named sessions e2e " + secrets.token_hex(4)
    sent = result(
        one.call(
            "send_message",
            {"to": ["cursor.lane-b@mai.internal"], "subject": subject, "body": "From lane-a to lane-b.", "request_id": "it-" + secrets.token_hex(6)},
            session=one.ha,
        ),
        "send_message",
    )
    check(sent.get("ref"), "send_message gave no ref")
    ok("send_message with session A to cursor.lane-b@mai.internal")

    # The bridge writes the notice file at each heartbeat (30 s). The mail is not
    # notified yet, thus the hook shows it.
    end = time.monotonic() + 75
    lines = []
    while time.monotonic() < end:
        p = subprocess.run([MAI, "hook", "--format", "plain", "--agent", "cursor"], capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=30)
        check(p.returncode == 0, f"mai hook gave exit {p.returncode}")
        lines = [x for x in p.stdout.splitlines() if x.strip()]
        if any("cursor.lane-b" in x for x in lines):
            break
        time.sleep(2)
    check(lines == ["mai: session cursor.lane-b has 1 new mail message. Use the mai tools to read the mail."], f"mai hook printed {lines}")
    ok("mai hook --format plain shows one line: cursor.lane-b has 1 new mail message")
    p = subprocess.run([MAI, "hook", "--format", "claude", "--event", "UserPromptSubmit", "--agent", "cursor"], capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=30)
    out = json.loads(p.stdout)
    check("cursor.lane-b" in json.dumps(out) and "cursor.lane-a" not in json.dumps(out), f"mai hook --format claude printed {out}")
    ok("mai hook --format claude --event UserPromptSubmit shows the mail of cursor.lane-b in JSON")

    got = result(one.call("wait_for_mail", {"timeout_sec": 30}, session=one.hb, timeout=60), "wait_for_mail B")
    check(not got["timed_out"] and any(m["subject"] == subject for m in got["messages"]), f"wait_for_mail B gave {len(got['messages'])} messages")
    check(got["address"].startswith("cursor.lane-b@"), f"wait_for_mail B is for {got['address']}")
    ok("wait_for_mail with session B gives the mail")
    inbox = result(one.call("list_inbox", session=one.hb), "list_inbox B")
    check(any(m["subject"] == subject for m in inbox["messages"]), "the mail is not in the inbox of lane-b")
    ok("list_inbox with session B has the mail")
    inbox = result(one.call("list_inbox", session=one.ha), "list_inbox A")
    check(inbox["address"].startswith("cursor.lane-a@"), f"list_inbox A is for {inbox['address']}")
    check(not any(m["subject"] == subject for m in inbox["messages"]), "the mail is in the inbox of lane-a")
    ok("list_inbox with session A does not have the mail")

    unknown = "ses-" + "a" * 16
    check(error_code(one.call("whoami", session=unknown), "whoami unknown") == "SESSION_UNKNOWN", "an unknown handle")
    ok("whoami with an unknown handle gives SESSION_UNKNOWN")


def run_second(one, two):
    two.request("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "it2", "version": "0"}})
    two.notify("notifications/initialized")
    code = error_code(two.call("login", {"session": "lane-a"}), "login lane-a in bridge two")
    check(code == "SESSION_BUSY", f"login lane-a in bridge two gave {code}")
    ok("a second bridge that logs in to lane-a gets SESSION_BUSY")
    exit_code = two.close()
    check(exit_code == 0, f"bridge two gave exit {exit_code}")
    ok("bridge two stops with exit 0 when its stdin closes")

    table = mai("session", "list")
    print(table.rstrip(), flush=True)
    s = sessions()
    for login in ("cursor.lane-a", "cursor.lane-b"):
        check(s[login]["leased"] is True, f"{login} is not leased")
        check(s[login]["pid"] == one.proc.pid, f"the lease of {login} has the pid {s[login]['pid']}")
        check(os.path.realpath(s[login]["folder"]) == os.path.realpath(one.folder), f"the folder of {login} is {s[login]['folder']}")
        check(s[login]["label"] == login.split(".")[1] and s[login]["status"] == "active", f"{login}: {s[login]}")
    check(s[one.pool]["leased"] is True, f"the pool session {one.pool} is not leased")
    check(" yes " in next(x for x in table.splitlines() if x.startswith("cursor.lane-a ")), "the table does not show lane-a as leased")
    ok("mai session list shows lane-a and lane-b as leased by bridge one, with its pid and folder")

    r = one.call("logout", {"session": one.hb})
    out = result(r, "logout B")
    check(out["session"] == one.hb and out["login"] == "cursor.lane-b", f"logout B gave {out}")
    ok("logout of B gives the handle B and cursor.lane-b")
    check(error_code(one.call("whoami", session=one.hb), "whoami B after logout") == "SESSION_UNKNOWN", "B after logout")
    ok("whoami with session B after the logout gives SESSION_UNKNOWN")
    check(sessions()["cursor.lane-b"]["leased"] is False, "lane-b is leased after the logout")
    ok("mai session list shows lane-b as not leased after the logout")

    check(mai("session", "revoke", "cursor.lane-b", "--yes").strip() == "cursor.lane-b", "mai session revoke")
    check(sessions()["cursor.lane-b"]["status"] == "revoked", "lane-b is not revoked")
    ok("mai session revoke cursor.lane-b --yes")
    code = error_code(one.call("login", {"session": "lane-b"}), "login lane-b after revoke")
    check(code == "SESSION_REVOKED", f"login lane-b after the revoke gave {code}")
    ok("login lane-b after the revoke gives SESSION_REVOKED")


def run_end(one):
    check(mai("session", "rotate", "cursor.lane-a").strip() == "cursor.lane-a", "mai session rotate")
    check(sessions()["cursor.lane-a"]["leased"] is False, "lane-a is leased after the rotate")
    ok("mai session rotate cursor.lane-a ends the lease of lane-a")
    w = result(one.call("whoami", session=one.ha), "whoami A after rotate")
    check(w["login"] == "cursor.lane-a", f"whoami A after the rotate is {w['login']}")
    check(sessions()["cursor.lane-a"]["leased"] is True, "lane-a has no new lease")
    ok("whoami with session A after the rotate gives cursor.lane-a: the bridge got a new lease")

    notice = os.path.join(os.environ["MAI_HOME"], "state", "notices", f"{os.getpid()}-cursor.json")
    check(os.path.exists(notice), "bridge one has no notice file")
    exit_code = one.close()
    check(exit_code == 0, f"bridge one gave exit {exit_code}")
    check(not os.path.exists(notice), "the notice file stays after the stop of bridge one")
    left = [x for x, v in sessions().items() if v["leased"]]
    check(not left, f"leases stay after the stop of bridge one: {left}")
    ok("bridge one stops with exit 0, removes its notice file, and ends each of its leases")


try:
    main()
except Fail as e:
    print(f"FAIL: {e}", file=sys.stderr, flush=True)
    sys.exit(1)
PY
python3 -I "$work/driver.py" "$mai" "$work" || fail "the bridge test"

"$mai" uninstall --instance "$instance" --yes >"$work/uninstall.log" 2>&1 ||
  { cat "$work/uninstall.log" >&2; fail "mai uninstall"; }
echo "ok: mai uninstall --instance $instance --yes"

[ ! -e "$mai" ] || fail "the binary stays"
for d in config state cache; do
  [ ! -e "$MAI_HOME/$d" ] || fail "the folder MAI_HOME/$d stays"
done
[ -z "$(docker ps -aq --filter "label=dev.mai.instance=$instance")" ] || fail "a container of $instance stays"
[ -z "$(docker volume ls -q --filter "label=dev.mai.instance=$instance")" ] || fail "a volume of $instance stays"
if docker container inspect "$instance" >/dev/null 2>&1; then fail "the container $instance stays"; fi
for v in "${instance}3-data" "${instance}3-spool"; do
  if docker volume inspect "$v" >/dev/null 2>&1; then fail "the volume $v stays"; fi
done
if [ "$(uname)" = Darwin ]; then
  for a in "owner@$instance" "agent:cursor@$instance"; do
    if security find-generic-password -s "$MAI_TEST_KEYCHAIN_SERVICE" -a "$a" >/dev/null 2>&1; then
      fail "the keychain item $a stays"
    fi
  done
fi
docker image inspect "$image" >/dev/null 2>&1 || fail "mai uninstall removed the image $image, which it did not pull"
echo "ok: no container, volume, keychain item, folder, or binary of $instance stays, and the image $image stays"
echo "all named session tests passed"
