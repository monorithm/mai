# Reference: configuration

The app reads these environment variables. The container sets the values for
production.

| Key | Meaning | Necessary | Default |
|---|---|---|---|
| `DATABASE_BACKEND` | The storage backend: `memory` or `postgres`. Production uses `postgres`. | No | `postgres` in the container |
| `DATABASE_DSN` | The PostgreSQL connection string. | Yes, with `postgres` | The socket of the container |
| `MAI_DATA` | The data directory: database, mail and secrets. | No | `/data` |
| `MAI_ETC` | The directory of the configuration templates of Dovecot and Postfix. | No | The `etc` directory of the image |
| `MAI_SECRETS` | The directory of the account secrets. | No | In `MAI_DATA` |
| `HTTP_PORT` | The port of the HTTP server in the container. | No | `8080` |
| `MAI_HOSTS` | More `Host` values for the allowlist, separated by commas. | No | Empty |
| `MAI_ORIGINS` | More `Origin` values for the allowlist, separated by commas. | No | Empty |
| `MAI_CREDENTIAL_FILE` | The credential file for the login on stdio, as a path in the container. | No | Empty |
| `MAI_CREDENTIAL` | The text of a credential for the login on stdio. The server removes it at start. | No | Empty |
| `MAI_CLIENT` | The name of the MCP client. | No | Empty |
| `MAI_BOARD_CRED` | The credential that the board uses to send notices. | No | Empty |
| `MAI_DOVEADM_SOCKET` | The socket of the doveadm server. The app sends each doveadm mail command to it, because the app user cannot read the mail files. The socket accepts only `move` and the `acl` commands. | No | `/run/dovecot/doveadm-server` |
| `MAI_BACKUP` | `off` stops the daily backup of the controller at 02:00 UTC. | No | `on` |
| `MAI_PG_SUPERUSER` | The PostgreSQL superuser for backup and restore. | No | `postgres` |
| `THREADS_AGENT_REFRESH` | The time in seconds between two reads of the agent list. The queue router starts a worker for each new agent after this time. | No | `30` |

The file `.env.example` gives an example.

## Configuration file of the CLI

The host CLI reads the file `config.json` in its configuration folder. A
missing file or a missing field gets the default.

| Field | Meaning | Default |
|---|---|---|
| `instance` | The name of the container. The volumes are `<instance>3-data` and `<instance>3-spool`. | `mai` |
| `port` | The port on `127.0.0.1` for the HTTP endpoint of the container. | `8765` |
| `image` | The image of the container. | `ghcr.io/monorithm/mai:<version of the CLI>` |
| `data_dir` | A folder on the host for `/data`. Empty gives the volume `<instance>3-data`. `mai start --data-dir PATH` sets it. | Empty |
| `approval` | The approval rule of a new lease: `each-bridge` or `app-instance`. | `each-bridge` |

Example:

```json
{
  "instance": "mai",
  "port": 8765,
  "image": "ghcr.io/monorithm/mai:3.0.0",
  "data_dir": "",
  "approval": "each-bridge"
}
```

## Folders of the CLI

| OS | Configuration | State | Cache and log |
|---|---|---|---|
| macOS | `~/Library/Application Support/mai` | `~/Library/Application Support/mai/state` | `~/Library/Caches/mai` |
| Linux | `$XDG_CONFIG_HOME/mai` | `$XDG_STATE_HOME/mai` | `$XDG_CACHE_HOME/mai` |
| Windows | `%AppData%\mai` | `%LocalAppData%\mai\state` | `%LocalAppData%\mai\cache` |

The state folder holds the manifest `manifest.json` and the notice files of
the bridges. No folder holds a secret.

The Postfix queue is always on the volume `<instance>3-spool`, at
`/var/lib/mai/spool` in the container. A host folder on macOS or Windows can
hold no Unix socket, and the queue has the sockets of Postfix, Dovecot, and the
milter.
