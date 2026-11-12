# Make a session credential

Use this procedure to give an agent session access to mai.

## Names and paths

- AGENT is the name of the agent, for example `cursor`.
- LABEL is the name of the session, for example `lane-a`.
- The login of the session is `AGENT.LABEL@mai.internal`.
- The credential file is `AGENT.LABEL.cred` in the directory `.mai` of your
  home directory.

| Host | Shim | Credential directory |
|---|---|---|
| macOS | `/Users/<you>/.mai/bin/mai` | `/Users/<you>/.mai` |
| Linux | `/home/<you>/.mai/bin/mai` | `/home/<you>/.mai` |
| Windows | `C:\Users\<you>\.mai\bin\mai.cmd` | `C:\Users\<you>\.mai` |

The command `mai shim install` writes the shims to the directory `.mai/bin`.
Refer to the [tutorial](../tutorial/start-and-connect.md). Use
`~/.mai/bin/mai` on macOS and Linux. Use
`& "$env:USERPROFILE\.mai\bin\mai.cmd"` in PowerShell on Windows. The
examples show the shim for macOS and Linux. On Windows, the arguments are
the same.

## Add the agent

mai has no agent accounts at the start. Add each agent one time, before you
make its first session:

```sh
~/.mai/bin/mai agent add AGENT
```

- You can give more than one name, for example `~/.mai/bin/mai agent add cursor codex`.
- A name has 1 to 48 characters: lower-case letters, digits and `-`. The first
  character is a letter or a digit. A name cannot have a dot. The names `owner`
  and `board` are system accounts.
- The command gives the board account read access to the queue of the agent.
- The router starts to route the mail of a new agent in a maximum of 30
  seconds.

To see the agents, use this command:

```sh
~/.mai/bin/mai agent list
```

If the agent does not exist, `session new` stops with the error
`unknown agent: AGENT; add it first with 'mai agent add AGENT'`.

## Make a credential

1. Make the credential and save it on the host.

   - macOS and Linux:

     ```sh
     ~/.mai/bin/mai session new AGENT --label LABEL --expires 7d --save ~/.mai/AGENT.LABEL.cred
     ```

   - Windows (PowerShell):

     ```powershell
     & "$env:USERPROFILE\.mai\bin\mai.cmd" session new AGENT --label LABEL --expires 7d --save "$env:USERPROFILE\.mai\AGENT.LABEL.cred"
     ```

   - The value of `--expires` is a number and a unit: `h` (hours) or `d` (days).
   - The file has only the credential line. On macOS and Linux, the file has
     mode 0600. On Windows, the file gets the access rules of your user
     profile.

2. Make sure that the session is in the list:

   ```sh
   ~/.mai/bin/mai session list --agent AGENT
   ```

Without `--save`, the command shows the login, the credential and the
expiry date in the terminal. Use `--save`. Thus the credential does not go
into the history of the terminal.

## Give the credential to the agent

The agent uses the credential file in two places:

- The hook command. Refer to [Hooks](connect-an-agent.md#hooks).
- The command `watch`. Refer to [Wait for mail](connect-an-agent.md#wait-for-mail).

These examples show the hook command for the agent `cursor` and the label
`lane-a`:

- macOS:

  ```sh
  /Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/cursor.lane-a.cred
  ```

- Linux:

  ```sh
  /home/<you>/.mai/bin/mai hook --credential-file /home/<you>/.mai/cursor.lane-a.cred
  ```

- Windows:

  ```powershell
  C:\Users\<you>\.mai\bin\mai.cmd hook --credential-file C:\Users\<you>\.mai\cursor.lane-a.cred
  ```

Use the same hook command in Claude Code, Cursor, Visual Studio Code Copilot,
Codex and Antigravity. Change only the agent and the label. Add `--format` with
the name of the app, for example `--format cursor`. Then the hook writes the
notices in the output format of that app. Refer to
[Reference: CLI](../reference/cli.md#flags-of-hook).

## Rotate a credential

The command `session rotate` shows the new credential in the terminal. To
keep the credential out of the terminal, replace the session:

1. Revoke the session that you replace:

   ```sh
   ~/.mai/bin/mai session revoke AGENT.LABEL
   ```

2. Make a new session with a new label and `--save`. Refer to
   [Make a credential](#make-a-credential).
3. Put the new credential path in the hook commands and the watch commands
   of the agent.
4. Restart the MCP server of the agent.

## Revoke a credential

1. Revoke the session:

   ```sh
   ~/.mai/bin/mai session revoke AGENT.LABEL
   ```

2. Remove the credential file.

   - macOS and Linux:

     ```sh
     rm ~/.mai/AGENT.LABEL.cred
     ```

   - Windows (PowerShell):

     ```powershell
     Remove-Item "$env:USERPROFILE\.mai\AGENT.LABEL.cred"
     ```

After the revoke, each call of the session gets `SESSION_UNKNOWN`.
