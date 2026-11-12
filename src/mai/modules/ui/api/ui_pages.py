"""The HTML pages of the owner UI. Each page is made on the server.

Only the sign-in page has a script, served from /ui/sign-in.js. No page shows
the HTML of a mail: the text of a mail is escaped.
"""

from __future__ import annotations

import html
import json

from bustan import HttpResponse

CSP = (
    "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; "
    "frame-ancestors 'none'; base-uri 'none'"
)

STYLE = b"""
body{font:15px/1.4 system-ui,sans-serif;margin:0 auto;max-width:1000px;padding:16px;color:#222;background:#fafafa}
nav a{margin-right:12px}
table{border-collapse:collapse;width:100%;margin:12px 0}
th,td{border-bottom:1px solid #ddd;padding:6px;text-align:left;vertical-align:top}
form.inline{display:inline}
.secret{font-family:monospace;background:#fffbe6;border:1px solid #e0c060;padding:8px;word-break:break-all}
.err{color:#a00}
pre.mail{white-space:pre-wrap;word-break:break-word}
input,button{font:inherit}
@media (prefers-color-scheme:dark){body{background:#151515;color:#ddd}th,td{border-color:#333}a{color:#8ab4f8}.secret{background:#332b00;border-color:#665500}}
"""


def base_headers() -> dict[str, str]:
    return {
        "content-security-policy": CSP,
        "x-content-type-options": "nosniff",
        "x-frame-options": "DENY",
        "referrer-policy": "no-referrer",
        "cache-control": "no-store",
    }


def e(v: object) -> str:
    return html.escape("" if v is None else str(v))


def page(
    status: int, title: str, body: str, headers: dict[str, str] | None = None
) -> HttpResponse:
    text = (
        "<!doctype html><html lang=en><head><meta charset=utf-8>"
        "<meta name=viewport content='width=device-width,initial-scale=1'>"
        f"<title>{e(title)}</title><link rel=stylesheet href='/ui/style.css'>"
        f"</head><body>{body}</body></html>"
    )
    h = base_headers()
    h.update(headers or {})
    return HttpResponse(
        status_code=status,
        headers=h,
        body=text.encode(),
        media_type="text/html; charset=utf-8",
    )


def style() -> HttpResponse:
    h = base_headers()
    h["cache-control"] = "max-age=3600"
    return HttpResponse(
        status_code=200, headers=h, body=STYLE, media_type="text/css; charset=utf-8"
    )


def redirect(location: str, headers: dict[str, str] | None = None) -> HttpResponse:
    h = base_headers()
    h["location"] = location
    h.update(headers or {})
    return HttpResponse(status_code=303, headers=h, body=b"", media_type="text/plain")


SIGN_IN_CSP = (
    "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self'; "
    "form-action 'none'; frame-ancestors 'none'; base-uri 'none'"
)

SIGN_IN_JS = b"""(function () {
  var m = /^#t=([A-Za-z0-9_-]+)$/.exec(location.hash);
  history.replaceState(null, "", location.pathname);
  var out = document.getElementById("state");
  if (!m) { out.textContent = "No ticket. Run the command: mai ui open"; return; }
  fetch("/ui/session", {
    method: "POST",
    headers: {"content-type": "application/json"},
    credentials: "same-origin",
    body: JSON.stringify({ticket: m[1]})
  }).then(function (r) {
    if (r.ok) { location.replace("/ui/mail"); }
    else { out.textContent = "The ticket is not valid. Run the command: mai ui open"; }
  });
})();
"""


def sign_in_page() -> HttpResponse:
    body = (
        "<h1>mai</h1><p id=state>Sign-in ...</p><script src='/ui/sign-in.js'></script>"
    )
    return page(200, "Sign in", body, {"content-security-policy": SIGN_IN_CSP})


def sign_in_script() -> HttpResponse:
    h = base_headers()
    return HttpResponse(
        status_code=200,
        headers=h,
        body=SIGN_IN_JS,
        media_type="text/javascript; charset=utf-8",
    )


def signed_out_page() -> HttpResponse:
    body = (
        "<h1>mai</h1><p>You are not signed in. Run the command"
        " <code>mai ui open</code> to sign in.</p>"
    )
    return page(401, "Signed out", body)


def nav(csrf: str) -> str:
    return (
        "<nav><a href='/ui/mail'>All mail</a><a href='/ui/threads'>Threads</a>"
        "<a href='/ui/agents'>Agents</a><a href='/ui/inbox'>Inbox</a>"
        "<a href='/ui/sessions'>Sessions</a><a href='/ui/board'>Board</a>"
        "<form class=inline method=post action='/ui/logout'>"
        f"<input type=hidden name=csrf value='{e(csrf)}'>"
        "<button>Sign out</button></form></nav>"
    )


def forbidden() -> HttpResponse:
    return page(403, "Forbidden", "<p class=err>The form token is not valid.</p>")


def not_found() -> HttpResponse:
    return page(404, "Not found", "<p>Not found.</p>")


def sessions_page(
    csrf: str,
    rows: list[dict],
    *,
    agents: list[str] | None = None,
    notice: str = "",
    secret_result: dict | None = None,
    error: str = "",
) -> HttpResponse:
    parts = ["<h1>Sessions</h1>", nav(csrf)]
    if error:
        parts.append(f"<p class=err>{e(error)}</p>")
    if notice:
        parts.append(f"<p>{e(notice)}</p>")
    if secret_result:
        parts.append(
            "<p>Copy the credential now. The server shows it one time only.</p>"
            f"<div class=secret>{e(secret_result.get('credential'))}</div>"
            f"<p>Login {e(secret_result.get('login'))}, expires {e(secret_result.get('expires_at'))}</p>"
        )
    parts.append(
        "<table><tr><th>Login</th><th>Agent</th><th>Status</th><th>Expires</th><th>Last login</th><th></th></tr>"
    )
    for r in rows:
        login = r.get("login", "")
        actions = ""
        if r.get("status") in ("issued", "active", "expired"):
            for act in ("revoke",):
                actions += (
                    f"<form class=inline method=post action='/ui/sessions/{act}'>"
                    f"<input type=hidden name=csrf value='{e(csrf)}'>"
                    f"<input type=hidden name=login value='{e(login)}'>"
                    f"<button>{act.capitalize()}</button></form> "
                )
        parts.append(
            f"<tr><td>{e(login)}</td><td>{e(r.get('agent'))}</td><td>{e(r.get('status'))}</td>"
            f"<td>{e(r.get('expires_at'))}</td><td>{e(r.get('last_login_at'))}</td><td>{actions}</td></tr>"
        )
    parts.append("</table>")
    return page(200, "Sessions", "".join(parts))


def board_page(csrf: str, snapshot: dict | None, error: str = "") -> HttpResponse:
    parts = ["<h1>Board</h1>", nav(csrf)]
    if error:
        parts.append(f"<p class=err>Cannot read the board: {e(error)}</p>")
    elif snapshot:
        parts.append(f"<pre>{e(json.dumps(snapshot, indent=2, default=str))}</pre>")
    else:
        parts.append("<p>The board is empty.</p>")
    return page(200, "Board", "".join(parts))


def mail_list_page(
    csrf: str,
    title: str,
    base: str,
    rows: list,
    *,
    search: str | None = None,
    error: str = "",
    envelopes: dict[str, dict] | None = None,
) -> HttpResponse:
    parts = [f"<h1>{e(title)}</h1>", nav(csrf)]
    if search is not None:
        parts.append(
            f"<form method=get action='{e(base)}'><label>Search subject and sender "
            f"<input name=q value='{e(search)}'></label> <button>Search</button></form>"
        )
    if error:
        parts.append(f"<p class=err>{e(error)}</p>")
        return page(200, title, "".join(parts))
    if not rows:
        parts.append("<p>There is no mail.</p>")
        return page(200, title, "".join(parts))
    head = "<th>Received</th><th>From</th><th>Subject</th>"
    if envelopes is not None:
        head += "<th>Envelope</th>"
    parts.append(f"<table><tr>{head}</tr>")
    for m in rows:
        cells = (
            f"<td>{e(m.received)}</td><td>{e(m.sender)}</td>"
            f"<td><a href='{e(base)}/{e(m.uid)}'>{e(m.subject or '(no subject)')}</a></td>"
        )
        if envelopes is not None:
            env = envelopes.get(m.message_id)
            cells += f"<td>{e(_envelope_text(env)) if env else ''}</td>"
        parts.append(f"<tr>{cells}</tr>")
    parts.append("</table>")
    return page(200, title, "".join(parts))


def _envelope_text(env: dict) -> str:
    keys = [k for k in env if k not in ("message_id", "id")]
    return "; ".join(f"{k}: {env[k]}" for k in keys)


def mail_page(csrf: str, title: str, back: str, mail) -> HttpResponse:
    parts = [f"<h1>{e(title)}</h1>", nav(csrf), f"<p><a href='{e(back)}'>Back</a></p>"]
    parts.append("<table>")
    for name, value in mail.headers:
        parts.append(f"<tr><th>{e(name)}</th><td>{e(value)}</td></tr>")
    if mail.envelope:
        parts.append(
            f"<tr><th>Envelope</th><td>{e(_envelope_text(mail.envelope))}</td></tr>"
        )
    parts.append(f"</table><pre class=mail>{e(mail.body)}</pre>")
    return page(200, title, "".join(parts))


def agents_page(csrf: str, agents: list[str], error: str = "") -> HttpResponse:
    parts = ["<h1>Agents</h1>", nav(csrf)]
    if error:
        parts.append(f"<p class=err>{e(error)}</p>")
    if not agents:
        parts.append("<p>There are no agents.</p>")
    else:
        parts.append("<ul>")
        parts.extend(f"<li><a href='/ui/agents/{e(a)}'>{e(a)}</a></li>" for a in agents)
        parts.append("</ul>")
    return page(200, "Agents", "".join(parts))


def threads_page(csrf: str, claims: list) -> HttpResponse:
    parts = ["<h1>Threads</h1>", nav(csrf)]
    if not claims:
        parts.append("<p>No thread has a claim.</p>")
        return page(200, "Threads", "".join(parts))
    parts.append(
        "<table><tr><th>Agent</th><th>Thread</th><th>Holder</th>"
        "<th>Lease end</th><th>State</th></tr>"
    )
    for c in claims:
        state = "claimed" if c.live else "expired"
        parts.append(
            f"<tr><td>{e(c.agent)}</td><td>{e(c.root_msg_id)}</td><td>{e(c.holder)}</td>"
            f"<td>{e(c.lease_end)}</td><td>{e(state)}</td></tr>"
        )
    parts.append("</table>")
    return page(200, "Threads", "".join(parts))
