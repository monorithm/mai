#!/bin/sh
# End-to-end test of the named sessions: the host CLI, the bridge, and the hook
# against a mai image. A chat uses a named session with a login and a handle,
# or with only the label of the session as the argument session.
# Usage: scripts/it_named_sessions.sh [IMAGE]   (default mai:wf-ns-e2e)
# MAI_IT_NAME sets the instance (default mai-wf-ns-e2e), and MAI_IT_PORT sets
# the host port (default 18990).
#
# The script builds mai into a scratch folder. MAI_HOME is a scratch folder,
# thus the key file is in that folder. With MAI_HOME, the CLI uses no item of
# the keychain. The script refuses the instance "mai" and the port 8765. It
# never pulls an image and never prints a key. At the end, mai uninstall
# removes the instance, and the script does a check that no object stays and
# that the key file has no key of the instance. It needs Go, Python 3, and a
# container engine.
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
keyfile="$MAI_HOME/state/keys.json"

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

(cd "$root/cli" && go build -o "$mai" ./cmd/mai) || fail "go build"
echo "ok: build mai"

# keyfile_check MODE ACCOUNT...: with MODE "has", the key file has each account,
# the mode 0600, and a folder with the mode 0700. With MODE "none", the key
# file does not exist or has no account of the instance. It prints no key.
keyfile_check() {
  python3 -I - "$keyfile" "$instance" "$@" <<'PY'
import json, os, stat, sys

path, instance, mode, accounts = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
if mode == "none":
    if not os.path.exists(path):
        sys.exit(0)
    with open(path) as f:
        names = list(json.load(f).get("keys", {}))
    left = [a for a in names if a.rsplit("@", 1)[-1] == instance]
    if left:
        sys.exit("the key file has keys of the instance: " + ", ".join(left))
    sys.exit(0)
for p, want in ((path, 0o600), (os.path.dirname(path), 0o700)):
    got = stat.S_IMODE(os.stat(p).st_mode)
    if got != want:
        sys.exit(f"{p} has the mode {got:04o}, not {want:04o}")
with open(path) as f:
    names = set(json.load(f).get("keys", {}))
missing = [a for a in accounts if a not in names]
if missing:
    sys.exit("the key file has no key for " + ", ".join(missing))
PY
}

"$mai" start --instance "$instance" --port "$port" --image "$image" --no-pull >"$work/start.log" 2>&1 ||
  { tail -20 "$work/start.log" >&2; fail "mai start"; }
echo "ok: mai start --instance $instance --port $port --image $image --no-pull"

[ "$("$mai" agent add cursor)" = cursor ] || fail "mai agent add cursor"
echo "ok: mai agent add cursor"
keyfile_check has "owner@$instance" "agent:cursor@$instance" || fail "the key file"
echo "ok: the key file has the owner key and the key of cursor, with the mode 0600 in a folder with the mode 0700"

for label in lane-a lane-b lane-c; do
  out=$("$mai" session new cursor --label "$label") || fail "mai session new cursor --label $label"
  [ "$out" = "cursor.$label" ] || fail "mai session new cursor --label $label printed $out"
  echo "ok: mai session new cursor --label $label prints only cursor.$label"
done
if "$mai" session new cursor --label lane-a >/dev/null 2>"$work/dup.log"; then
  fail "a second mai session new cursor --label lane-a did not fail"
fi
grep -q SESSION_EXISTS "$work/dup.log" || fail "a second session new gave no SESSION_EXISTS"
echo "ok: a second mai session new cursor --label lane-a gives SESSION_EXISTS"
if "$mai" session new cursor --label s1 >/dev/null 2>"$work/pool-label.log"; then
  fail "mai session new cursor --label s1 did not fail"
fi
grep -q BAD_LABEL "$work/pool-label.log" || fail "mai session new cursor --label s1 gave no BAD_LABEL"
echo "ok: mai session new cursor --label s1 gives BAD_LABEL, because s1 is a label of the pool"

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
SESSION_PATTERN = r"^(ses-[a-z2-7]{16}|([a-z0-9][a-z0-9-]{0,47}\.)?[a-z0-9][a-z0-9-]{0,31})$"
POOL = re.compile(r"^cursor\.s[0-9]+$")
INIT = {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "it", "version": "0"}}
# Each bridge that the driver starts. At the end, the driver stops each of them.
BRIDGES = []


class Fail(Exception):
    pass


def ok(text):
    print("ok: " + text, flush=True)


def check(cond, text):
    if not cond:
        raise Fail(text)


def wait_until(cond, text, timeout=5):
    """Examine cond until it is true, for a maximum of TIMEOUT seconds.

    A heartbeat of a bridge can change the state of a lease between two calls, thus each
    check of the state of a lease waits for the state.
    """
    end = time.monotonic() + timeout
    while True:
        if cond():
            return
        check(time.monotonic() < end, f"{text} (after {timeout} s)")
        time.sleep(0.2)


class Bridge:
    """One bridge process. The driver is its app, thus the parent of the bridge."""

    def __init__(self, name):
        BRIDGES.append(self)
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
        return "\n".join(self.text().splitlines()[-20:])

    def text(self):
        """The log of the bridge (its stderr)."""
        self.log.flush()
        with open(self.log_path, "rb") as f:
            return f.read().decode(errors="replace")


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


def leased(login):
    return sessions()[login]["leased"] is True


def main():
    try:
        one = Bridge("one")
        run(one)
        run_labels(one)
        two = Bridge("two")
        run_second(one, two)
        run_end(one)
        run_pool_full()
    except Exception:
        for b in BRIDGES:
            print(f"--- bridge {b.name} log ---\n{b.tail()}", file=sys.stderr)
        raise
    finally:
        for b in BRIDGES:
            b.close()


def run(one):
    init = one.request("initialize", INIT)
    check(init["serverInfo"]["version"] == "3.2.0", f"the contract is {init['serverInfo']}")
    check('session: "lane-1"' in init["instructions"], "the instructions do not tell to give the label as session")
    check("login" in init["instructions"], "the instructions do not tell about login")
    one.notify("notifications/initialized")
    ok("initialize gives the contract 3.2.0, and the instructions tell to give the label as session")

    guide = one.request("resources/read", {"uri": "mai://guide"})
    check("login" in guide["contents"][0]["text"], "the guide does not tell about login")
    ok("resources/read mai://guide tells about login")

    tools = {t["name"]: t for t in one.request("tools/list")["tools"]}
    check({"login", "logout"} <= set(tools), "tools/list has no login or logout")
    check(tools["login"]["inputSchema"]["properties"]["session"]["type"] == "string", "login has no session string")
    check(tools["login"]["inputSchema"].get("required") == ["session"], "login does not require session")
    check(tools["logout"]["inputSchema"]["properties"]["session"]["type"] == "string", "logout has no session string")
    check(tools["logout"]["inputSchema"]["properties"]["session"].get("pattern") == SESSION_PATTERN, "logout does not take a label")
    for name, t in tools.items():
        if name in ("login", "logout"):
            continue
        prop = t["inputSchema"]["properties"].get("session")
        check(prop is not None and prop.get("type") == "string" and prop.get("pattern") == SESSION_PATTERN, f"{name} has no session property")
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


def session_error(r, what):
    """The code of a tool error with the path session."""
    code = error_code(r, what)
    check(r["structuredContent"]["error"].get("path") == "session", f"{what}: the path of {code} is not session")
    return code


def run_labels(one):
    """A chat gives only the label of its session in each call, with no login."""
    w = result(one.call("whoami", session="lane-c"), "whoami lane-c")
    check(w["login"] == "cursor.lane-c", f"whoami with the label lane-c is {w['login']}")
    ok("whoami with session lane-c and no login gives cursor.lane-c")
    wait_until(
        lambda: leased("cursor.lane-c") and sessions()["cursor.lane-c"]["pid"] == one.proc.pid,
        "lane-c has no lease of bridge one",
    )
    ok("mai session list shows lane-c as leased by bridge one")

    subject = "label e2e " + secrets.token_hex(4)
    body = "From lane-a to lane-c."
    result(
        one.call(
            "send_message",
            {"to": ["cursor.lane-c@mai.internal"], "subject": subject, "body": body, "request_id": "it-" + secrets.token_hex(6)},
            session="lane-a",
        ),
        "send_message lane-a",
    )
    ok("send_message with session lane-a, the label of the login A, to cursor.lane-c@mai.internal")
    got = result(one.call("wait_for_mail", {"timeout_sec": 30}, session="lane-c", timeout=60), "wait_for_mail lane-c")
    check(got["address"].startswith("cursor.lane-c@"), f"wait_for_mail lane-c is for {got['address']}")
    mail = next((m for m in got["messages"] if m["subject"] == subject), None)
    check(mail is not None, f"wait_for_mail lane-c gave {len(got['messages'])} messages and not the mail")
    read = result(one.call("read_message", {"ref": mail["ref"]}, session="lane-c"), "read_message lane-c")
    check(read["message"]["body"].strip() == body, "read_message lane-c gave a different body")
    ok("wait_for_mail and read_message with session lane-c give the mail")
    result(
        one.call(
            "reply",
            {"ref": mail["ref"], "body": "From lane-c to lane-a.", "request_id": "it-" + secrets.token_hex(6)},
            session="cursor.lane-c",
        ),
        "reply cursor.lane-c",
    )
    ok("reply with session cursor.lane-c")

    def reply_from_lane_c():
        inbox = result(one.call("list_inbox", session="lane-a"), "list_inbox lane-a")
        return any(subject in m["subject"] and "cursor.lane-c" in m["from"] for m in inbox["messages"])

    wait_until(reply_from_lane_c, "lane-a has no reply from cursor.lane-c", timeout=30)
    ok("list_inbox with session lane-a has the reply from cursor.lane-c")

    h = result(one.call("login", {"session": "lane-c"}), "login lane-c")
    check(HANDLE.match(h["session"]) and h["login"] == "cursor.lane-c", f"login lane-c gave {h}")
    w = result(one.call("whoami", session=h["session"]), "whoami with the handle of lane-c")
    check(w["login"] == "cursor.lane-c", f"whoami with the handle of lane-c is {w['login']}")
    leases = one.text().count("a chat logged in to the session cursor.lane-c")
    check(leases == 1, f"bridge one got {leases} leases for lane-c")
    ok("login lane-c gives the handle of the lease of the calls with the label: bridge one got one lease for lane-c")

    notice = os.path.join(os.environ["MAI_HOME"], "state", "notices", f"{os.getpid()}-cursor.json")
    wait_until(lambda: os.path.exists(notice) and "cursor.lane-c" in open(notice).read(), "the notice file has no cursor.lane-c", timeout=45)
    ok("the notice file of bridge one has cursor.lane-c")

    for value, want in (("s1", "BAD_LABEL"), ("cursor.s2", "BAD_LABEL"), ("lane-z", "SESSION_UNKNOWN"), ("Lane-C", "INVALID_ARGUMENT")):
        code = session_error(one.call("whoami", session=value), f"whoami {value}")
        check(code == want, f"whoami with the session {value} gave {code}, not {want}")
    ok("whoami with s1 or cursor.s2 gives BAD_LABEL, with lane-z SESSION_UNKNOWN, and with Lane-C INVALID_ARGUMENT")

    out = result(one.call("logout", {"session": "lane-c"}), "logout lane-c")
    check(out["session"] == h["session"] and out["login"] == "cursor.lane-c", f"logout lane-c gave {out}")
    wait_until(lambda: not leased("cursor.lane-c"), "lane-c is leased after the logout")
    code = session_error(one.call("logout", {"session": "lane-c"}), "a second logout lane-c")
    check(code == "SESSION_UNKNOWN", f"a second logout lane-c gave {code}")
    ok("logout with session lane-c ends the lease of lane-c, and a second logout gives SESSION_UNKNOWN")


def run_second(one, two):
    two.request("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "it2", "version": "0"}})
    two.notify("notifications/initialized")
    code = error_code(two.call("login", {"session": "lane-a"}), "login lane-a in bridge two")
    check(code == "SESSION_BUSY", f"login lane-a in bridge two gave {code}")
    ok("a second bridge that logs in to lane-a gets SESSION_BUSY")
    code = session_error(two.call("whoami", session="lane-a"), "whoami lane-a in bridge two")
    check(code == "SESSION_BUSY", f"whoami with session lane-a in bridge two gave {code}")
    ok("a call with session lane-a in a second bridge gets SESSION_BUSY")
    exit_code = two.close()
    check(exit_code == 0, f"bridge two gave exit {exit_code}")
    ok("bridge two stops with exit 0 when its stdin closes")

    wait_until(lambda: leased("cursor.lane-a") and leased("cursor.lane-b"), "lane-a and lane-b are not leased")
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
    wait_until(lambda: not leased("cursor.lane-b"), "lane-b is leased after the logout")
    ok("mai session list shows lane-b as not leased after the logout")

    check(mai("session", "revoke", "cursor.lane-b", "--yes").strip() == "cursor.lane-b", "mai session revoke")
    wait_until(lambda: sessions()["cursor.lane-b"]["status"] == "revoked", "lane-b is not revoked")
    ok("mai session revoke cursor.lane-b --yes")
    code = error_code(one.call("login", {"session": "lane-b"}), "login lane-b after revoke")
    check(code == "SESSION_REVOKED", f"login lane-b after the revoke gave {code}")
    ok("login lane-b after the revoke gives SESSION_REVOKED")


def renews(bridge, login):
    """The number of new leases for LOGIN in the log of the bridge."""
    return bridge.text().count(f"the bridge has a new lease for the session {login}")


def live_leases():
    return [x for x, v in sessions().items() if v["leased"]]


def run_end(one):
    before = renews(one, "cursor.lane-a")
    check(mai("session", "rotate", "cursor.lane-a").strip() == "cursor.lane-a", "mai session rotate")
    # A heartbeat of bridge one can get a new lease for lane-a before this check. The log of
    # the bridge then has one more new lease for lane-a.
    wait_until(
        lambda: not leased("cursor.lane-a") or renews(one, "cursor.lane-a") > before,
        "lane-a is leased after the rotate, and bridge one got no new lease for it",
    )
    ok("mai session rotate cursor.lane-a ends the lease of lane-a")
    w = result(one.call("whoami", session=one.ha), "whoami A after rotate")
    check(w["login"] == "cursor.lane-a", f"whoami A after the rotate is {w['login']}")
    wait_until(lambda: leased("cursor.lane-a"), "lane-a has no new lease")
    ok("whoami with session A after the rotate gives cursor.lane-a: the bridge got a new lease")

    notice = os.path.join(os.environ["MAI_HOME"], "state", "notices", f"{os.getpid()}-cursor.json")
    check(os.path.exists(notice), "bridge one has no notice file")
    exit_code = one.close()
    check(exit_code == 0, f"bridge one gave exit {exit_code}")
    check(not os.path.exists(notice), "the notice file stays after the stop of bridge one")
    wait_until(lambda: not live_leases(), "leases stay after the stop of bridge one")
    ok("bridge one stops with exit 0, removes its notice file, and ends each of its leases")


def run_pool_full():
    """A bridge starts and serves named sessions when the pool of its agent is full."""
    pool = []
    for i in range(1, 9):
        p = Bridge(f"pool{i}")
        pool.append(p)
        p.request("initialize", INIT)
        p.notify("notifications/initialized")
        w = result(p.call("whoami"), f"whoami of bridge pool{i}")
        check(POOL.match(w["login"] or ""), f"whoami of bridge pool{i} is {w['login']}")
        p.pool = w["login"]
    logins = sorted({p.pool for p in pool})
    check(len(logins) == 8, f"the 8 pool bridges have the pool sessions {logins}")
    ok("8 bridges hold the 8 pool sessions of cursor")

    full = Bridge("full")
    init = full.request("initialize", INIT)
    check(init["serverInfo"]["version"] == "3.2.0", f"the contract is {init['serverInfo']}")
    full.notify("notifications/initialized")
    wait_until(lambda: "no free pool session" in full.text(), "the log of the bridge does not tell that no pool session is free")
    ok("a ninth bridge starts with a pool lease with no session, and initialize gives the contract 3.2.0")
    tools = {t["name"] for t in full.request("tools/list")["tools"]}
    check({"login", "logout", "whoami"} <= tools, f"tools/list gave {sorted(tools)}")
    ok("tools/list works on the pool lease with no session")
    r = full.call("whoami")
    check(error_code(r, "whoami with no free pool session") == "NO_FREE_SESSION", f"whoami with no session gave {r}")
    check(r["structuredContent"]["error"].get("retry_safe") is True, f"NO_FREE_SESSION is not retry_safe: {r}")
    ok("whoami with no session gives NO_FREE_SESSION with retry_safe true")

    c = result(full.call("login", {"session": "lane-c"}), "login lane-c")
    check(HANDLE.match(c["session"]) and c["login"] == "cursor.lane-c", f"login lane-c gave {c}")
    w = result(full.call("whoami", session=c["session"]), "whoami C")
    check(w["login"] == "cursor.lane-c", f"whoami C is {w['login']}")
    ok("login lane-c and whoami with its handle work in the bridge with no pool session")
    for name in ("s2", "cursor.s2"):
        code = error_code(full.call("login", {"session": name}), f"login {name}")
        check(code == "BAD_LABEL", f"login {name} gave {code}")
        code = session_error(full.call("whoami", session=name), f"whoami {name}")
        check(code == "BAD_LABEL", f"whoami with the session {name} gave {code}")
    ok("login and whoami with s2 or cursor.s2 give BAD_LABEL: a named session is never a pool session")
    w = result(full.call("whoami", session="lane-c"), "whoami lane-c in the bridge with no pool session")
    check(w["login"] == "cursor.lane-c", f"whoami with the label lane-c is {w['login']}")
    ok("whoami with the label lane-c uses the lease of the login in the bridge with no pool session")

    last = pool[-1]
    exit_code = last.close()
    check(exit_code == 0, f"bridge {last.name} gave exit {exit_code}")
    wait_until(lambda: not leased(last.pool), f"{last.pool} is leased after the stop of bridge {last.name}")
    w = result(full.call("whoami"), "whoami after a pool session is free")
    check(w["login"] == last.pool, f"whoami after the bind is {w['login']}, not {last.pool}")
    wait_until(lambda: leased(last.pool) and sessions()[last.pool]["pid"] == full.proc.pid, f"{last.pool} has no lease of the ninth bridge")
    ok(f"whoami with no session binds the free pool session {last.pool} to the pool lease of the ninth bridge")
    wait_until(lambda: f"the pool lease has the session {last.pool}" in full.text(), "no heartbeat gave the pool session", timeout=45)
    ok(f"a heartbeat gives the session {last.pool}, and the ninth bridge records it")

    for b in [*pool[:-1], full]:
        exit_code = b.close()
        check(exit_code == 0, f"bridge {b.name} gave exit {exit_code}")
    wait_until(lambda: not live_leases(), "leases stay after the stop of the bridges")
    ok("each bridge stops with exit 0 and ends each of its leases")


try:
    main()
except Fail as e:
    print(f"FAIL: {e}", file=sys.stderr, flush=True)
    sys.exit(1)
PY
python3 -I "$work/driver.py" "$mai" "$work" || fail "the bridge test"

# The server CLI also refuses a label of the pool.
if "$mai" exec -- mai session new cursor --label s1 >/dev/null 2>"$work/server-label.log"; then
  fail "the server CLI made the session cursor.s1 with --label s1"
fi
grep -q BAD_LABEL "$work/server-label.log" || fail "the server CLI gave no BAD_LABEL for --label s1"
echo "ok: the server CLI mai session new cursor --label s1 gives BAD_LABEL"

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
for a in "owner@$instance" "agent:cursor@$instance"; do
  grep -q "^gone     key $a\$" "$work/uninstall.log" || fail "mai uninstall did not remove the key $a from the key file"
done
keyfile_check none || fail "the key file after mai uninstall"
docker image inspect "$image" >/dev/null 2>&1 || fail "mai uninstall removed the image $image, which it did not pull"
echo "ok: mai uninstall removed the keys of $instance from the key file"
echo "ok: no container, volume, key, folder, or binary of $instance stays, and the image $image stays"
echo "all named session tests passed"
