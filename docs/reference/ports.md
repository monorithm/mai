# Reference: ports

| Port in the container | Port on the host | Service | Protocol |
|---|---|---|---|
| 8080 | 127.0.0.1:8765 | App: MCP (`POST /mcp`), the API (`/api`), and the optional mail UI (`/ui`) | HTTP |
| 1993 | Not published | Dovecot | IMAP with TLS |
| 1587 | Not published | Postfix submission | SMTP with STARTTLS |
| 1143 | Not published | Dovecot, for the app in the container | IMAP |
| 5432 | Not published | PostgreSQL, socket in the container | PostgreSQL |

The CLI publishes only the port 8080, on 127.0.0.1 only. The `port` field of
the configuration file of the CLI changes the host port. Do not publish the
port on other addresses.

## HTTP rules

- `GET` and `DELETE` on `/mcp` get 405.
- The `Host` header must be in the allowlist.
- An `Origin` header that is not in the allowlist gets 403.
- The maximum size of a request is 1 MB.
