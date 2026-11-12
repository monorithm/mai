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
| `MAI_CREDENTIAL` | The text of a credential for the login on stdio. The host shim sets it from `MAI_CREDENTIAL_FILE` on the host. The server removes it at start. | No | Empty |
| `MAI_CLIENT` | The name of the MCP client. | No | Empty |
| `MAI_BOARD_CRED` | The credential that the board uses to send notices. | No | Empty |
| `MAI_DOVEADM_SOCKET` | The socket of the doveadm server. The app sends each doveadm mail command to it, because the app user cannot read the mail files. The socket accepts only `move` and the `acl` commands. | No | `/run/dovecot/doveadm-server` |
| `MAI_BACKUP` | `off` stops the daily backup of the controller at 02:00 UTC. | No | `on` |
| `MAI_PG_SUPERUSER` | The PostgreSQL superuser for backup and restore. | No | `postgres` |
| `THREADS_AGENT_REFRESH` | The time in seconds between two reads of the agent list. The queue router starts a worker for each new agent after this time. | No | `30` |

The file `.env.example` gives an example.

## Shim settings on the host

The shims `mai`, `mai.cmd` and `mai.ps1` read these environment
variables on the host. The app does not read them.

| Key | Meaning | Necessary | Default |
|---|---|---|---|
| `MAI_CONTAINER` | The name of the container that the shim uses with `docker exec`. | No | `mai` |
| `MAI_DOCKER` | The docker binary. Give a full path if docker is not in `PATH`. | No | `docker` |
| `MAI_CREDENTIAL_FILE` | The credential file on the host for `watch` and `hook`, when the command has no `--credential-file`. For `mcp`, the shim reads this file and gives its text to the container in `MAI_CREDENTIAL`. | No | Empty |

## Compose settings on the host

The file `compose.yaml` reads this environment variable on the host. The
command `mai shim install` writes this file to `~/.mai/bin`. In a clone, the
file is in the root directory.

| Key | Meaning | Necessary | Default |
|---|---|---|---|
| `MAI_TAG` | The tag of the image `ghcr.io/monorithm/mai`: `latest`, a release such as `2.0.0`, `2.0` or `2`, or `sha-<short>`. `docker compose pull` gets this tag. `docker compose build` in a clone gives this tag to the local image. | No | `latest` |

To pin a version, set `MAI_TAG` before the compose command.

- macOS and Linux:

  ```sh
  MAI_TAG=2.0.0 docker compose -f ~/.mai/bin/compose.yaml pull
  MAI_TAG=2.0.0 docker compose -f ~/.mai/bin/compose.yaml up -d
  ```

- Windows (PowerShell):

  ```powershell
  $env:MAI_TAG = "2.0.0"
  docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" pull
  docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" up -d
  ```

The image is public. For a pull, a login and a token are not necessary.

The shim does not send the host variables to the container. For `mcp`, it
sends only `MAI_CREDENTIAL` and `MAI_CLIENT`. Refer to [CLI](cli.md).
