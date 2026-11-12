"""Fakes of the mail session for the threads and notices tests."""

import threading

from mai.modules.threads.domain.returner import ThreadMailReturner

ROOT = "<msg-root@mai.internal>"
REPLY = "<msg-reply@mai.internal>"
OTHER = "<msg-other@mai.internal>"


def headers(mid, refs=None, sender="architect", kind="request", prio="high"):
    h = {
        "message-id": mid,
        "from": f"{sender}@mai.internal",
        "received": f"from x by mail (Authenticated sender: {sender}) with ESMTPSA",
        "x-mai-kind": kind,
        "x-mai-priority": prio,
        "subject": "secret subject",
    }
    if refs:
        h["references"] = refs
    return h


class FakeMail:
    def __init__(self, boxes, copyuid=True):
        self.boxes = boxes  # mailbox -> {uid: (headers, flags set)}
        self.copyuid = copyuid
        self.next_uid = 100
        self.idle_calls = 0

    def search(self, mailbox, criteria):
        box = self.boxes.get(mailbox, {})
        if criteria.startswith("UID "):
            uid = int(criteria.split()[1])
            return [uid] if uid in box else []
        out = []
        for uid, (h, flags) in box.items():
            if "UNKEYWORD $MaiNotified" in criteria and "$MaiNotified" in flags:
                continue
            if "UNSEEN" in criteria and "\\Seen" in flags:
                continue
            out.append(uid)
        return sorted(out)

    def contains_uid(self, mailbox, uid):
        return uid in self.boxes.get(mailbox, {})

    def fetch_headers(self, mailbox, uids):
        box = self.boxes.get(mailbox, {})
        return [
            {"uid": u, "flags": sorted(box[u][1]), "headers": box[u][0], "size": 1}
            for u in uids
            if u in box
        ]

    def move(self, mailbox, uid, dest):
        item = self.boxes[mailbox].pop(uid)
        self.next_uid += 1
        self.boxes.setdefault(dest, {})[self.next_uid] = item
        return self.copyuid

    def store_flags(self, mailbox, uids, add=(), remove=()):
        for u in uids:
            self.boxes[mailbox][u][1].update(add)
            self.boxes[mailbox][u][1].difference_update(remove)

    def idle(self, mailboxes, timeout):
        self.idle_calls += 1
        return False

    def close(self):
        pass


class State:
    def __init__(self, login, mail):
        self.login = login
        self.agent = login.split(".")[0]
        self.mail = mail
        self.handle = login
        self.lock = threading.RLock()


def queue():
    return {
        "shared/cursor/INBOX": {
            1: (headers(ROOT), set()),
            2: (headers(REPLY, refs=ROOT), set()),
            3: (headers(OTHER), set()),
        }
    }


class RecordingReturner(ThreadMailReturner):
    """A fake of ThreadMailReturner: it records each return and gives ok."""

    def __init__(self, ok=True):
        self.calls = []
        self.ok = ok

    def __call__(self, session_login: str, agent: str, root: str) -> bool:
        self.calls.append((session_login, agent, root))
        return self.ok
