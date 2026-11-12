import imaplib
import smtplib
import ssl
import subprocess
import sys
import time
import uuid
from email.message import EmailMessage


def cred(path):
    with open(path) as f:
        parts = f.read().strip().split(":")
    return parts[1], parts[2]


lane_login, lane_secret = cred("/data/lanea.cred")
snd_login, snd_secret = cred("/data/sender.cred")
ctx = ssl._create_unverified_context()


def send(subject, mid, reply_to=None):
    m = EmailMessage()
    m["From"] = f"{snd_login}@mai.internal"
    m["To"] = "cursor@mai.internal"
    m["Subject"] = subject
    m["Message-ID"] = mid
    if reply_to:
        m["In-Reply-To"] = reply_to
        m["References"] = reply_to
    m.set_content("test")
    s = smtplib.SMTP("127.0.0.1", 1587)
    s.starttls(context=ctx)
    s.login(snd_login, snd_secret)
    s.send_message(m)
    s.quit()


def count(box, mid):
    i = imaplib.IMAP4("127.0.0.1", 1143)
    i.starttls(ssl_context=ctx)
    i.login(lane_login, lane_secret)
    i.select(f'"{box}"', readonly=True)
    _typ, data = i.search(None, "HEADER", "Message-ID", mid)
    i.logout()
    return len(data[0].split())


root = f"<{uuid.uuid4().hex}@mai.internal>"
send("root", root)
time.sleep(2)
print("root in queue:", count("shared/cursor/INBOX", root))
print(sys.argv[1] if len(sys.argv) > 1 else "")
sql = (
    "INSERT INTO mail_auth.thread_claim(agent,root_msg_id,login,lease_end) VALUES "
    f"('cursor','{root}','{lane_login}',now()+interval '1 hour')"
)
r = subprocess.run(
    ["psql", "-h", "/run/postgresql", "-U", "postgres", "-d", "mai", "-c", sql],
    check=False,
    capture_output=True,
    text=True,
)
print("claim:", r.returncode, r.stderr[-200:])
reply = f"<{uuid.uuid4().hex}@mai.internal>"
send("Re: root", reply, root)
t0 = time.time()
ok = False
while time.time() - t0 < 5:
    if count("INBOX", reply) == 1 and count("shared/cursor/INBOX", reply) == 0:
        ok = True
        break
    time.sleep(0.5)
print("ROUTED", ok, round(time.time() - t0, 1))
