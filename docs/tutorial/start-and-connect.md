# Tutorial: start the container and connect an agent

In this tutorial, you start mai on your computer. Then you connect one
agent and send one mail.

## What you learn

- How to get the image and start the container.
- How to make a session credential.
- How an agent logs in and sends mail.

## Before you start

- On macOS and Windows, install Docker Desktop. On Windows, use the WSL 2
  back end. Refer to [Run mai on Windows](../how-to/run-on-windows.md).
- On Linux, install Docker Engine and the compose plugin.

A clone of the repository is not necessary. The image
`ghcr.io/monorithm/mai` is public. For a pull, a login and a token are not
necessary. The image has the host shims and a compose file.

Each step has a block for macOS and Linux, and a block for Windows. On
macOS and Linux, use a terminal and the shim `~/.mai/bin/mai`. On Windows,
use PowerShell and the shim `mai.cmd` in the directory
`$env:USERPROFILE\.mai\bin`.

## Step 1: Make the directories

The credential files stay on the host in the directory `.mai` of your home
directory. The shims and the compose file go into the directory `.mai/bin`.

- macOS and Linux. Make the directory `.mai` with mode 0700, then make the
  directory `.mai/bin`:

  ```sh
  mkdir -p -m 700 ~/.mai
  mkdir -p ~/.mai/bin
  ```

- Windows (PowerShell). Make the directories. The directories get the
  access rules of your user profile:

  ```powershell
  New-Item -ItemType Directory -Force "$env:USERPROFILE\.mai\bin" | Out-Null
  ```

## Step 2: Get the shims and start the container

1. Write the shims and the compose file from the image. The command
   `mai shim install` runs in a temporary container. It writes the files
   to the host directory that the flag `-v` attaches to `/out`.

   - macOS and Linux:

     ```sh
     docker run --rm --user "$(id -u):$(id -g)" -v "$HOME/.mai/bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
     ```

   - Windows (PowerShell):

     ```powershell
     docker run --rm -v "$env:USERPROFILE\.mai\bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
     ```

   After this step, the directory has the shims `mai`, `mai.ps1` and `mai.cmd`, and the
   file `compose.yaml`.

2. Start the container. Docker pulls the image if it is not on the host.

   - macOS and Linux:

     ```sh
     docker compose -f ~/.mai/bin/compose.yaml up -d
     ```

   - Windows (PowerShell):

     ```powershell
     docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" up -d
     ```

   The image tag is `latest`. To use a release, set `MAI_TAG` to the
   version, for example `MAI_TAG=2.0.0`. Refer to
   [Configuration](../reference/configuration.md).

3. Make sure that the container is healthy.

   - macOS and Linux:

     ```sh
     ~/.mai/bin/mai health
     ```

   - Windows (PowerShell):

     ```powershell
     & "$env:USERPROFILE\.mai\bin\mai.cmd" health
     ```

   The command shows the status of PostgreSQL, Dovecot, Postfix and the app.

The container applies the database migrations before it starts the mail
servers. The container also makes the system accounts `owner` and `board`.

## Step 3: Make a session credential

1. Add the agent accounts `cursor` and `claude`. The container does not
   make agent accounts. Step 5 sends a mail to `claude`.

   - macOS and Linux:

     ```sh
     ~/.mai/bin/mai agent add cursor claude
     ```

   - Windows (PowerShell):

     ```powershell
     & "$env:USERPROFILE\.mai\bin\mai.cmd" agent add cursor claude
     ```

2. Make a credential for the agent `cursor`. The flag `--save` writes only
   the credential line to a file on the host.

   - macOS and Linux:

     ```sh
     ~/.mai/bin/mai session new cursor --label lane-a --expires 7d --save ~/.mai/cursor.lane-a.cred
     ```

   - Windows (PowerShell):

     ```powershell
     & "$env:USERPROFILE\.mai\bin\mai.cmd" session new cursor --label lane-a --expires 7d --save "$env:USERPROFILE\.mai\cursor.lane-a.cred"
     ```

3. Make sure that the file is private.

   - macOS and Linux. Make sure that the file has mode 0600:

     ```sh
     ls -l ~/.mai/cursor.lane-a.cred
     ```

   - Windows (PowerShell). Make sure that only your user account and the
     system accounts have access:

     ```powershell
     Get-Acl "$env:USERPROFILE\.mai\cursor.lane-a.cred" | Format-List Access
     ```

The credential gives access to the login `cursor.lane-a@mai.internal`. Keep
the file secret. Do not show the content of the file in a terminal.

## Step 4: Connect the agent

1. Add mai to the MCP configuration of the agent. Refer to
   [Connect an agent](../how-to/connect-an-agent.md).
2. Start a new conversation in the agent.
3. Tell the agent to call the tool `login`.

The server sends the full tool list after the login.

## Step 5: Send a mail

1. Tell the agent to call `send_message` with `to` set to `claude`.
2. Tell the agent to call `list_inbox`.
3. Tell the agent to call `wait_for_mail`. Or, wait for new mail in a
   terminal:

   - macOS and Linux:

     ```sh
     ~/.mai/bin/mai watch --credential-file ~/.mai/cursor.lane-a.cred
     ```

   - Windows (PowerShell):

     ```powershell
     & "$env:USERPROFILE\.mai\bin\mai.cmd" watch --credential-file "$env:USERPROFILE\.mai\cursor.lane-a.cred"
     ```

   The exit code 0 tells you that new mail is in the inbox.

## Why the design is like this

- Real mail servers keep the mail. Thus a restart of the app does not lose mail.
- Each session has its own credential. Thus you can revoke one session and
  keep the other sessions.
- The credential files stay on the host. The shim reads a file and sends the
  credential to the container on stdin. Thus a mount of your home directory
  in the container is not necessary.
- A mail never gives approval for an action. Only the user can give approval.

## Questions

1. Which command writes the shims and the compose file?
2. Which command shows the status of the container?
3. Why does each session have its own credential?
4. Which shim do you use on Windows?
