# Connect an agent

Use this procedure to connect an agent app to mai. The apps are Claude Code
(`claude`), Cursor (`cursor`), Visual Studio Code Copilot (`copilot`), Codex
(`codex`), and Antigravity (`antigravity`).

## Before you start

- Install the CLI `mai`. Refer to the quick start in the README.
- Start mai with `mai start`.

## Procedure

1. Add an agent account:

   ```sh
   mai agent add cursor
   ```

   The CLI keeps the agent key in the system keychain. It does not show the
   key.

2. Connect the app to the agent:

   ```sh
   mai connect cursor --agent cursor
   ```

   The CLI shows the change to the MCP entry and the hooks of the app. Type
   `y` to agree. The CLI writes the change and records it in its manifest.

3. Start the app, or start the MCP server again in the app.
4. When the app starts the bridge, the OS shows a prompt for Touch ID,
   Windows Hello, or your password. Make sure that the prompt shows the
   correct agent, app, and folder. Then approve it.

   > [!WARNING]
   > Refuse a prompt that you did not expect. A different program can start
   > the bridge.

5. In the app, tell the agent to call the tool `whoami`. The result gives the
   session of the lease, for example `cursor.s1`.

## The MCP entry

`mai connect` writes this entry. It holds no secret:

```json
{
  "mcpServers": {
    "mai": {
      "command": "mai",
      "args": ["mcp", "--agent", "cursor"]
    }
  }
}
```

The bridge `mai mcp` gets a lease from the server and bridges stdio to
`http://127.0.0.1:8765/mcp`. The server gives a free session of the agent. The
bridge cannot select a session.

## Hooks

`mai connect` also writes the hooks of the app. The hook command is
`mai hook --format F --event E`. It reads the notice file of its bridge and
shows the count of new mail. It never calls the server, and on an error it
shows nothing.

## Disconnect an app

To remove the entries that `mai connect` wrote, run this command:

```sh
mai disconnect cursor
```

## Sessions and leases

| Command | Action |
|---|---|
| `mai session list` | Show the sessions, their leases, and the app and folder of each lease. |
| `mai session end LOGIN` | End the lease of a session. The bridge gets an error at its next call. |
