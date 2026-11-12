#!/bin/sh
# Container test of the mail UI: the state on and off, the ticket sign-in, and
# the All mail view of the copies mailbox.
# Usage: scripts/it_ui.sh [IMAGE]   (default mai:wf-ui)
# Build the image first: docker build --pull=false -t mai:wf-ui .
# The script never pulls an image and never prints a secret. It removes the
# container and its volumes at the end.
set -eu
image=${1:-mai:wf-ui}
name=mai-wf-ui-it
volume=mai-wf-ui-it-data
port=18780
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" "$volume-spool" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker volume create "$volume" >/dev/null
docker volume create "$volume-spool" >/dev/null
docker run -d --pull=never --name "$name" --read-only \
  --label dev.mai.managed-by=mai-it \
  --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
  --cap-add SETGID --cap-add SETUID --cap-add KILL \
  --tmpfs /run --tmpfs /tmp --tmpfs /etc/postfix --tmpfs /etc/dovecot \
  --tmpfs /var/lib/postgresql \
  -e MAI_HOSTS="127.0.0.1:$port" -p "127.0.0.1:$port:8080" \
  -v "$volume:/data" -v "$volume-spool:/var/lib/mai/spool" "$image" >/dev/null
i=0
until docker exec "$name" mai health >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then docker logs "$name" 2>&1 | tail -40; exit 1; fi
  sleep 1
done
docker exec "$name" mai agent add cursor >/dev/null

# The copies principal, its ACL, and always_bcc. When the server of this tree
# makes them already, these steps change nothing.
docker exec "$name" psql -h /run/postgresql -U postgres -d mai -qtAc \
  "INSERT INTO mail_auth.principal (login, kind, status) VALUES ('copies', 'system', 'active') ON CONFLICT (login) DO NOTHING" >/dev/null
docker exec "$name" doveadm mailbox create -u copies INBOX >/dev/null 2>&1 || true
docker exec "$name" doveadm acl set -u copies INBOX user=owner \
  lookup read write write-seen write-deleted expunge
if [ "$(docker exec "$name" postconf -h always_bcc)" != "copies@mai.internal" ]; then
  docker exec "$name" postconf -e always_bcc=copies@mai.internal
  # Postfix can start after the health check. Try the reload again until it works.
  j=0
  until docker exec "$name" postfix reload >/dev/null 2>&1; do
    j=$((j + 1))
    if [ "$j" -gt 30 ]; then echo "it_ui: postfix reload failed"; exit 1; fi
    sleep 1
  done
fi

docker exec -i "$name" python3 - <<'PY'
import http.client
import json
import os
import secrets
import subprocess
import time

PATH = "/data/secrets/owner.cred"


def owner_key() -> str:
    """Read the owner key of the 2.x file, or set a new key with mai owner set-key."""
    if os.path.exists(PATH):
        with open(PATH) as f:
            return f.read().strip()
    alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
    key = "mai1:owner:" + "".join(secrets.choice(alphabet) for _ in range(43))
    subprocess.run(
        ["mai", "owner", "set-key", "--replace"],
        input=key + "\n",
        text=True,
        check=True,
        capture_output=True,
    )
    return key


OWNER = owner_key()


def req(method, path, body=None, headers=None):
    c = http.client.HTTPConnection("127.0.0.1", 8080, timeout=30)
    h = {"host": "127.0.0.1:8080", **(headers or {})}
    data = None
    if body is not None:
        data = json.dumps(body)
        h["content-type"] = "application/json"
    c.request(method, path, data, h)
    r = c.getresponse()
    return r, r.read().decode("utf-8", "replace")


auth = {"authorization": "Bearer " + OWNER}
failures = []


def check(label, ok):
    print(("ok  " if ok else "FAIL") + " " + label)
    if not ok:
        failures.append(label)


r, b = req("GET", "/api/ui", headers=auth)
check("the UI is off by default", r.status == 200 and json.loads(b) == {"enabled": False})
r, _ = req("GET", "/ui/sign-in")
check("a /ui route gives 404 when off", r.status == 404)
r, b = req("POST", "/api/ui/tickets", headers=auth)
check("a ticket gives 409 UI_OFF when off", r.status == 409 and "UI_OFF" in b)
r, b = req("PUT", "/api/ui", {"enabled": True}, auth)
check("PUT /api/ui turns the UI on", r.status == 200 and json.loads(b) == {"enabled": True})
r, b = req("POST", "/api/ui/tickets", headers=auth)
check("a ticket is made", r.status == 201)
ticket = json.loads(b)["ticket"]
r, b = req("GET", "/ui/sign-in")
check("the sign-in page has its script", r.status == 200 and "/ui/sign-in.js" in b)
r, _ = req("POST", "/ui/session", {"ticket": ticket})
cookie_header = r.getheader("set-cookie") or ""
check(
    "the ticket gives a strict cookie",
    r.status == 204
    and "HttpOnly" in cookie_header
    and "SameSite=Strict" in cookie_header
    and "Path=/ui" in cookie_header,
)
cookie = {"cookie": cookie_header.split(";")[0]}
r, _ = req("POST", "/ui/session", {"ticket": ticket})
check("the ticket is valid one time", r.status == 401)

subject = "ui-it-" + secrets.token_hex(4)
mail = (
    "From: owner@mai.internal\nTo: cursor@mai.internal\n"
    f"Subject: {subject}\n\nhello <b>cursor</b>\n"
)
subprocess.run(
    ["sendmail", "-f", "owner@mai.internal", "cursor@mai.internal"],
    input=mail,
    text=True,
    check=True,
)
found = ""
for _ in range(30):
    r, found = req("GET", "/ui/mail", headers=cookie)
    if subject in found:
        break
    time.sleep(1)
check("All mail lists the delivered mail", r.status == 200 and subject in found)
r, b = req("GET", "/ui/agents/cursor", headers=cookie)
check("the queue of cursor lists the mail", r.status == 200 and subject in b)

r, _ = req("PUT", "/api/ui", {"enabled": False}, auth)
r, _ = req("GET", "/ui/mail", headers=cookie)
check("each /ui route gives 404 after off", r.status == 404)
raise SystemExit(1 if failures else 0)
PY
echo "it_ui: all checks passed"
