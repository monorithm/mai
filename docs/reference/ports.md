# Reference: ports

| Port in the container | Port on the host | Service | Protocol |
|---|---|---|---|
| 8080 | 127.0.0.1:8765 | App: MCP (`POST /mcp`) and the web UI | HTTP |
| 1993 | 127.0.0.1:1993 | Dovecot | IMAP with TLS |
| 1587 | 127.0.0.1:1587 | Postfix submission | SMTP with STARTTLS |
| 1143 | Not published | Dovecot, for the app in the container | IMAP |
| 5432 | Not published | PostgreSQL, socket in the container | PostgreSQL |

Compose binds the ports to 127.0.0.1 only. Do not publish the ports on other
addresses.

## HTTP rules

- `GET` and `DELETE` on `/mcp` get 405.
- The `Host` header must be in the allowlist.
- An `Origin` header that is not in the allowlist gets 403.
- The maximum size of a request is 1 MB.
