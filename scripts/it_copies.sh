#!/bin/sh
# Start a mai container from an image and test the copy of each mail to the owner:
# the mailbox copies, the ACL of the owner, the copies rule of the milter, and the
# envelope record.
# Usage: scripts/it_copies.sh [IMAGE]   (default mai:wf-copies)
# Build the image first: docker build --pull=false -t mai:wf-copies .
# It removes the container and its volumes at the end. It never pulls an image.
# The credentials stay in the container: the checks read them there and print no secret.
set -eu
image=${1:-mai:wf-copies}
name=mai-wf-copies-it
volume=mai-wf-copies-data
port=18770
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" "$volume-spool" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker volume create "$volume" >/dev/null
docker volume create "$volume-spool" >/dev/null
docker run -d --pull=never --name "$name" --read-only \
  --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
  --cap-add SETGID --cap-add SETUID --cap-add KILL \
  --tmpfs /run --tmpfs /tmp --tmpfs /etc/postfix --tmpfs /etc/dovecot \
  --tmpfs /var/lib/postgresql \
  -e MAI_HOSTS="127.0.0.1:$port" -p "127.0.0.1:$port:8080" \
  -v "$volume:/data" -v "$volume-spool:/var/lib/mai/spool" "$image" >/dev/null
i=0
until docker exec "$name" mai health >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then docker logs "$name"; exit 1; fi
  sleep 1
done
docker exec "$name" mai agent add alpha beta gamma >/dev/null
docker exec "$name" sh -c '
  mai session new alpha --label s1 --file /data/alpha.cred >/dev/null
  mai session new beta --label s1 --file /data/beta.cred >/dev/null'
docker exec -i "$name" python - <<'EOF'
import imaplib
import re
import smtplib
import subprocess
import sys
import time

FAILED = []


def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + (f": {detail}" if detail else ""))
    if not ok:
        FAILED.append(name)


def credential(path):
    _prefix, login, secret = open(path).read().strip().split(":")
    return login, secret


def search(user, message_id):
    out = subprocess.run(
        ["doveadm", "-f", "flow", "search", "-u", user, "mailbox", "INBOX",
         "header", "Message-ID", message_id],
        capture_output=True, text=True, check=False,
    ).stdout
    return len([line for line in out.splitlines() if line.strip()])


def psql(sql):
    return subprocess.run(
        ["psql", "-X", "-At", "-h", "/run/postgresql", "-U", "mai", "-d", "mai",
         "-c", sql],
        capture_output=True, text=True, check=True,
    ).stdout.strip()


alpha_login, alpha_secret = credential("/data/alpha.cred")
owner_login, owner_secret = credential("/data/secrets/owner.cred")
message_id = "<it-copies-1@mai.internal>"
body = (
    f"From: {alpha_login}@mai.internal\r\n"
    "To: beta@mai.internal\r\n"
    "Subject: copies test\r\n"
    f"Message-ID: {message_id}\r\n"
    "\r\n"
    "hello\r\n"
)

# A mail from a session of alpha to beta, with gamma as a Bcc recipient.
smtp = smtplib.SMTP("127.0.0.1", 1588, timeout=30)
smtp.login(alpha_login, alpha_secret)
refused = smtp.sendmail(
    f"{alpha_login}@mai.internal", ["beta@mai.internal", "gamma@mai.internal"], body
)
check("the mail to beta and Bcc gamma is accepted", refused == {})

# A recipient copies@ is refused at RCPT TO with 5.7.1.
smtp.mail(f"{alpha_login}@mai.internal")
code, text = smtp.rcpt("copies@mai.internal")
check("RCPT TO copies@ is refused", code == 550 and b"5.7.1" in text, f"{code}")
smtp.rset()
smtp.quit()

deadline = time.monotonic() + 30
while time.monotonic() < deadline:
    if search("beta", message_id) and search("copies", message_id):
        break
    time.sleep(1)
check("the mail is in the queue of beta", search("beta", message_id) == 1)
check("the mail is in the queue of gamma", search("gamma", message_id) == 1)
check("one copy is in copies/INBOX", search("copies", message_id) == 1)

# The owner sees shared/copies and selects its INBOX.
imap = imaplib.IMAP4("127.0.0.1", 1143, timeout=30)
imap.login(owner_login, owner_secret)
_typ, listed = imap.list("", "shared/*")
names = [line.decode() for line in listed if line]
check("owner LIST shows shared/copies", any('"shared/copies"' in n or n.endswith(" shared/copies") for n in names))
typ, data = imap.select("shared/copies/INBOX", readonly=True)
count = int(data[0]) if typ == "OK" else -1
check("owner SELECT shared/copies/INBOX gives the count", typ == "OK" and count >= 1, f"{count} mail")
imap.logout()

# A session cannot select the copies.
imap = imaplib.IMAP4("127.0.0.1", 1143, timeout=30)
imap.login(alpha_login, alpha_secret)
_typ, listed = imap.list("", "shared/*")
names = [line.decode() for line in listed if line]
check("session LIST does not show shared/copies", not any("shared/copies" in n for n in names))
try:
    typ, data = imap.select("shared/copies/INBOX", readonly=True)
    detail = (data[0] or b"").decode() if data else ""
except imaplib.IMAP4.error as exc:
    typ, detail = "NO", str(exc)
check("session SELECT shared/copies/INBOX gives NONEXISTENT", typ == "NO" and "NONEXISTENT" in detail, typ)
imap.logout()

# No client can log in as copies.
imap = imaplib.IMAP4("127.0.0.1", 1143, timeout=30)
try:
    imap.login("copies", "x")
    check("login as copies is refused", False)
except imaplib.IMAP4.error:
    check("login as copies is refused", True)
imap.shutdown()

# The envelope record holds each recipient, also the Bcc recipient.
row = psql(
    "SELECT sender || '|' || array_to_string(recipients, ',') || '|' || "
    "coalesce(queue_id, '') FROM mail_auth.envelope "
    f"WHERE message_id = '{message_id}'"
)
parts = row.split("|") if row else []
check("one envelope row exists", len(row.splitlines()) == 1 and len(parts) == 3, row.split("|")[0] if row else "")
if len(parts) == 3:
    check("the envelope sender is alpha", parts[0] == f"{alpha_login}@mai.internal")
    check("the envelope has beta and the Bcc gamma", parts[1].split(",") == ["beta@mai.internal", "gamma@mai.internal"], parts[1])
    check("the envelope has the queue ID", re.fullmatch(r"[0-9A-Za-z]+", parts[2]) is not None)
check("no envelope row has copies@", psql("SELECT count(*) FROM mail_auth.envelope WHERE 'copies@mai.internal' = ANY(recipients)") == "0")

print("RESULT " + ("PASS" if not FAILED else "FAIL " + ", ".join(FAILED)))
sys.exit(1 if FAILED else 0)
EOF
