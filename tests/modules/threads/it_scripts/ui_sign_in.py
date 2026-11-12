import http.client
import urllib.parse

with open("/data/secrets/owner.cred") as f:
    cred = f.read().strip()


def req(method, path, body=None, headers=None):
    c = http.client.HTTPConnection("127.0.0.1", 8080)
    h = dict(headers or {})
    if body is not None:
        h["content-type"] = "application/x-www-form-urlencoded"
    c.request(method, path, body, h)
    r = c.getresponse()
    return r, r.read().decode()


r, b = req("POST", "/ui/login", urllib.parse.urlencode({"credential": cred}))
ck = (r.getheader("set-cookie") or "").split(";")[0]
print("login", r.status, r.getheader("location"), "failed" in b.lower())
r, b = req("GET", "/ui/sessions", headers={"cookie": ck})
print("sessions", r.status, "csrf" in b)
r, b = req(
    "POST",
    "/ui/sessions/revoke",
    urllib.parse.urlencode({"login": "copilot.w"}),
    {"cookie": ck},
)
print("nocsrf", r.status)
