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

   The CLI keeps the agent key in its key file. It does not show the key.

2. Connect the app to the agent:

   ```sh
   mai connect cursor --agent cursor
   ```

   The CLI shows the change to the MCP entry and the hooks of the app. Type
   `y` to agree. The CLI writes the change and records it in its manifest.

3. Start the app, or start the MCP server again in the app. The app starts
   the bridge, and the bridge gets a lease with the agent key. There is no
   prompt.
4. In the app, tell the agent to call the tool `whoami`. The result gives the
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
`http://127.0.0.1:8765/mcp`. The server gives a free pool session of the
agent, for example `cursor.s1`. A chat can also log in to a named session.

## Use a named session

One bridge can serve many chats, for example all the chats of one Cursor
window. To give a chat its own session, do these steps:

1. Make the named session:

   ```sh
   mai session new cursor --label lane-a
   ```

   The CLI shows only the login, `cursor.lane-a`.

2. In the chat, tell the agent to call the tool `login` with `session` set
   to `lane-a`. The bridge gives a handle, for example
   `ses-k7m2q4r5t6v3w2xa`.
3. Tell the agent to give the handle as the argument `session` in each tool
   call. A tool call with no `session` uses the pool session of the app.
4. To end the session of the chat, tell the agent to call the tool `logout`
   with the handle.

The handle is valid only in that bridge.

## Hooks

`mai connect` also writes the hooks of the app. The hook command is
`mai hook --format F --event E`. It reads the notice file of its bridge. It
shows one line for each session of the bridge that has new mail. It never
calls the server, and on an error it shows nothing.

## Disconnect an app

To remove the entries that `mai connect` wrote, run this command:

```sh
mai disconnect cursor
```

## Sessions and leases

| Command | Action |
|---|---|
| `mai session new AGENT --label L` | Make the named session `AGENT.L`. |
| `mai session new AGENT --count N` | Make each session of `AGENT.s1` to `AGENT.sN` that does not exist. |
| `mai session list` | Show the sessions with the label, the state, and the lease of each session. For a lease, show its app and folder. |
| `mai session rotate LOGIN` | Give the session a new internal secret, and end its lease. |
| `mai session revoke LOGIN` | Close the session, and end its lease. This change is permanent. |
| `mai session end LOGIN` | End the lease of a session. At its next call, the bridge gets a new lease for the same session. |
