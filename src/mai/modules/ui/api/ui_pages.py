"""The HTML pages of the owner UI. Each page is made on the server, with no script."""

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


def nav(csrf: str) -> str:
    return (
        "<nav><a href='/ui/sessions'>Sessions</a><a href='/ui/board'>Board</a>"
        "<form class=inline method=post action='/ui/logout'>"
        f"<input type=hidden name=csrf value='{e(csrf)}'>"
        "<button>Sign out</button></form></nav>"
    )


def login_page(error: str = "") -> HttpResponse:
    err = f"<p class=err>{e(error)}</p>" if error else ""
    body = (
        "<h1>mai owner</h1>" + err + "<form method=post action='/ui/login'>"
        "<label>Owner credential<br><input type=password name=credential "
        "autocomplete=off size=70 required></label><br><br>"
        "<button>Sign in</button></form>"
    )
    return page(401 if error else 200, "Sign in", body)


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
            for act in ("rotate", "revoke"):
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
    parts.append("</table><h2>New session</h2>")
    if not agents:
        parts.append(
            "<p>There are no agents. Add an agent with the command"
            " <code>mai agent add NAME</code>.</p>"
        )
        return page(200, "Sessions", "".join(parts))
    options = "".join(f"<option value='{e(a)}'>{e(a)}</option>" for a in agents)
    parts.append(
        "<form method=post action='/ui/sessions/new'>"
        f"<input type=hidden name=csrf value='{e(csrf)}'>"
        f"<label>Agent <select name=agent required>{options}</select></label> "
        "<label>Label <input name=label required pattern='[a-z0-9][a-z0-9-]*'></label> "
        "<label>Days <input name=days type=number min=1 max=90 value=7></label> "
        "<button>Create</button></form>"
    )
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
