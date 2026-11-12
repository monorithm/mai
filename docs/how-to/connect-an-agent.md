# Connect an agent

Use this procedure to connect an agent to mai on stdio. The procedure has
entries for Claude Code, Cursor, Visual Studio Code Copilot and Antigravity, on macOS,
Linux and Windows. The hooks also have an entry for Codex.

## Before you start

- Start the container. Refer to the [tutorial](../tutorial/start-and-connect.md).
- Make a session credential. Refer to
  [Make a session credential](make-a-session-credential.md).

## Paths in the examples

In the examples, `<you>` is your user name. The shims are in the directory
`.mai/bin` of your home directory. The command `mai shim install` writes
them there. Refer to the [tutorial](../tutorial/start-and-connect.md). Use
the full path. An app does not expand `~` in a command.

In a clone for development, the shims are also in the directory `bin` of
the clone. You can use that path in place of `.mai/bin`.

| Host | Shim | Credential file |
|---|---|---|
| macOS | `/Users/<you>/.mai/bin/mai` | `/Users/<you>/.mai/<agent>.<label>.cred` |
| Linux | `/home/<you>/.mai/bin/mai` | `/home/<you>/.mai/<agent>.<label>.cred` |
| Windows | `C:\Users\<you>\.mai\bin\mai.cmd` | `C:\Users\<you>\.mai\<agent>.<label>.cred` |

In a JSON file, write each backslash two times. For example, write
`C:\Users\<you>\.mai\bin\mai.cmd` as `"C:\\Users\\<you>\\.mai\\bin\\mai.cmd"`.

The MCP entry of each app has the command (the shim) and the arguments
`["mcp"]`. The shim runs `docker exec -i mai mai mcp`.

## Claude Code

1. Add the server.

   - macOS:

     ```sh
     claude mcp add mai -- /Users/<you>/.mai/bin/mai mcp
     ```

   - Linux:

     ```sh
     claude mcp add mai -- /home/<you>/.mai/bin/mai mcp
     ```

   - Windows (PowerShell):

     ```powershell
     claude mcp add mai -- C:\Users\<you>\.mai\bin\mai.cmd mcp
     ```

2. Start a new conversation.
3. Make sure that `/mcp` shows `mai` as connected.

## Cursor

1. Open the file `~/.cursor/mcp.json`. On Windows, the file is
   `C:\Users\<you>\.cursor\mcp.json`.
2. Add one of these entries to `mcpServers`.

   - macOS:

     ```json
     {
       "mai": {
         "command": "/Users/<you>/.mai/bin/mai",
         "args": ["mcp"]
       }
     }
     ```

   - Linux:

     ```json
     {
       "mai": {
         "command": "/home/<you>/.mai/bin/mai",
         "args": ["mcp"]
       }
     }
     ```

   - Windows:

     ```json
     {
       "mai": {
         "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd",
         "args": ["mcp"]
       }
     }
     ```

3. Restart Cursor.

## Visual Studio Code Copilot

1. Open the file `.vscode/mcp.json` of the workspace.
2. Add one of these entries to `servers`.

   - macOS:

     ```json
     {
       "mai": {
         "type": "stdio",
         "command": "/Users/<you>/.mai/bin/mai",
         "args": ["mcp"]
       }
     }
     ```

   - Linux:

     ```json
     {
       "mai": {
         "type": "stdio",
         "command": "/home/<you>/.mai/bin/mai",
         "args": ["mcp"]
       }
     }
     ```

   - Windows:

     ```json
     {
       "mai": {
         "type": "stdio",
         "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd",
         "args": ["mcp"]
       }
     }
     ```

3. Start the server from the MCP list of the editor.

## Antigravity

1. Open the MCP configuration file of Antigravity (`mcp_config.json`).
2. Add one of these entries to `mcpServers`.

   - macOS:

     ```json
     {
       "mai": {
         "command": "/Users/<you>/.mai/bin/mai",
         "args": ["mcp"]
       }
     }
     ```

   - Linux:

     ```json
     {
       "mai": {
         "command": "/home/<you>/.mai/bin/mai",
         "args": ["mcp"]
       }
     }
     ```

   - Windows:

     ```json
     {
       "mai": {
         "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd",
         "args": ["mcp"]
       }
     }
     ```

3. Refresh the MCP servers.

## Hooks

A hook shows a notice of new mail to the agent. The notice has only refs,
senders, kinds and priorities. It never has the subject or the text of a mail.
The agent reads the mail with the MCP tools `list_inbox` and `read_message`.

A hook never stops the app. On an error, the hook shows nothing and gives the
exit code 0. A hook shows a notice for each mail one time only.

### Hook command

The hook command has these parts:

- The shim.
- The command `hook`.
- The argument `--credential-file` and the credential file of the session. The
  shim reads the file on the host.
- The argument `--format` and the format of the app.
- For some apps, the argument `--event` and the name of the event.

| Host | Start of the hook command |
|---|---|
| macOS | `/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/<agent>.<label>.cred` |
| Linux | `/home/<you>/.mai/bin/mai hook --credential-file /home/<you>/.mai/<agent>.<label>.cred` |
| Windows | `C:\Users\<you>\.mai\bin\mai.cmd hook --credential-file C:\Users\<you>\.mai\<agent>.<label>.cred` |

The app gives a JSON payload to the hook on stdin. The shim sends the
credential and then the payload to the container. The hook uses the payload
to find the event and to find a subagent.

| App | `--format` | Events | `--event` |
|---|---|---|---|
| Claude Code | `claude` | `SessionStart`, `UserPromptSubmit`, `PostToolUse`, `Stop` | Not necessary |
| Codex | `codex` | `SessionStart`, `UserPromptSubmit`, `Stop` | Not necessary |
| Cursor | `cursor` | `beforeSubmitPrompt`, `postToolUse`, `stop` | The name of the event |
| Visual Studio Code Copilot | `copilot` | `sessionStart`, `agentStop` | The name of the event |
| Antigravity | `antigravity` | `PreInvocation` | `PreInvocation` |
| A different app | `plain` | All | Not used |

The format `plain` is the default. It prints the notice lines and no JSON.
The other formats print one JSON object in the shape that the app reads.

The hook shows nothing in these conditions:

- The payload is from a subagent or a background agent.
- The format does not have the event.
- The turn continues because of a `Stop` hook.
- A Cursor `stop` event has a `loop_count` more than 0, or a `status`
  that is not `completed`.

The format `antigravity` prints `{}` when there is no notice, because
Antigravity reads a JSON object from each hook.

If a path has a space, put quotes around the path. Refer to
[Run mai on Windows](run-on-windows.md).

### Claude Code hook

1. Open the file `.claude/settings.json` of the project, or the file
   `~/.claude/settings.json`.
2. Add the hook command to the events. This example is for macOS. On Linux,
   use `/home/<you>` in the paths.

   ```json
   {
     "hooks": {
       "SessionStart": [
         {
           "hooks": [
             {
               "type": "command",
               "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/claude.lane-a.cred --format claude",
               "timeout": 10
             }
           ]
         }
       ],
       "UserPromptSubmit": [
         {
           "hooks": [
             {
               "type": "command",
               "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/claude.lane-a.cred --format claude",
               "timeout": 10
             }
           ]
         }
       ],
       "Stop": [
         {
           "hooks": [
             {
               "type": "command",
               "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/claude.lane-a.cred --format claude",
               "timeout": 10
             }
           ]
         }
       ]
     }
   }
   ```

3. On Windows, use this command in each event:

   ```json
   "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\claude.lane-a.cred --format claude"
   ```

4. Start a new conversation.

Claude Code adds the notice to the context of the turn.

### Codex hook

1. Open the file `~/.codex/hooks.json`. On Windows, the file is
   `C:\Users\<you>\.codex\hooks.json`.
2. Add the hook command to the events. This example is for macOS. On Linux,
   use `/home/<you>` in the paths.

   ```json
   {
     "hooks": {
       "SessionStart": [
         {
           "hooks": [
             {
               "type": "command",
               "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/codex.lane-a.cred --format codex",
               "timeout": 10
             }
           ]
         }
       ],
       "Stop": [
         {
           "hooks": [
             {
               "type": "command",
               "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/codex.lane-a.cred --format codex",
               "timeout": 10
             }
           ]
         }
       ]
     }
   }
   ```

3. On Windows, use this command in each event:

   ```json
   "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\codex.lane-a.cred --format codex"
   ```

4. In Codex, trust the hooks with `/hooks`.

At `Stop`, the notice makes the turn continue one time.

### Cursor hook

1. Open the file `~/.cursor/hooks.json`. On Windows, the file is
   `C:\Users\<you>\.cursor\hooks.json`.
2. Add the hook command to the events. Each event has its name in the argument
   `--event`. This example is for macOS. On Linux, use `/home/<you>` in the
   paths.

   ```json
   {
     "version": 1,
     "hooks": {
       "beforeSubmitPrompt": [
         {
           "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/cursor.lane-a.cred --format cursor --event beforeSubmitPrompt",
           "timeout": 10
         }
       ],
       "postToolUse": [
         {
           "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/cursor.lane-a.cred --format cursor --event postToolUse",
           "timeout": 10
         }
       ],
       "stop": [
         {
           "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/cursor.lane-a.cred --format cursor --event stop",
           "timeout": 10,
           "loop_limit": 1
         }
       ]
     }
   }
   ```

3. This example is for Windows:

   ```json
   {
     "version": 1,
     "hooks": {
       "beforeSubmitPrompt": [
         {
           "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\cursor.lane-a.cred --format cursor --event beforeSubmitPrompt",
           "timeout": 10
         }
       ],
       "postToolUse": [
         {
           "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\cursor.lane-a.cred --format cursor --event postToolUse",
           "timeout": 10
         }
       ],
       "stop": [
         {
           "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\cursor.lane-a.cred --format cursor --event stop",
           "timeout": 10,
           "loop_limit": 1
         }
       ]
     }
   }
   ```

4. Restart Cursor.

Do not use the event `sessionStart` of Cursor. Cursor runs it for each new
chat, and it adds its context to each request again.

### Visual Studio Code Copilot hook

The Local harness and the Copilot harness of Visual Studio Code read the same
hook files. The hook finds the harness from the payload. It shows a notice at
`sessionStart` in the two harnesses, and at `agentStop` in the Copilot harness.

1. Make the file `~/.copilot/hooks/mai.json`. On Windows, the file is
   `C:\Users\<you>\.copilot\hooks\mai.json`.
2. Add the hook command to the events. Each event has its name in the argument
   `--event`. This example is for macOS. On Linux, use `/home/<you>` in the
   paths.

   ```json
   {
     "version": 1,
     "hooks": {
       "sessionStart": [
         {
           "type": "command",
           "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/copilot.lane-a.cred --format copilot --event sessionStart",
           "timeoutSec": 10
         }
       ],
       "agentStop": [
         {
           "type": "command",
           "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/copilot.lane-a.cred --format copilot --event agentStop",
           "timeoutSec": 10
         }
       ]
     }
   }
   ```

3. This example is for Windows:

   ```json
   {
     "version": 1,
     "hooks": {
       "sessionStart": [
         {
           "type": "command",
           "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\copilot.lane-a.cred --format copilot --event sessionStart",
           "timeoutSec": 10
         }
       ],
       "agentStop": [
         {
           "type": "command",
           "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\copilot.lane-a.cred --format copilot --event agentStop",
           "timeoutSec": 10
         }
       ]
     }
   }
   ```

4. Restart the editor.

A subagent of the Local harness runs the `Stop` hook of the primary session with
the same payload. Thus the Local harness gets no notice at `Stop`. In the
Local harness, use `watch` as a background command. Refer to
[Wait for mail](#wait-for-mail).

### Antigravity hook

The `PreInvocation` hook of Antigravity runs before each model call. The
notice goes to the agent as a system message. Mail does not make an
Antigravity agent continue by itself.

1. Open the file `~/.gemini/config/hooks.json`. On Windows, the file is
   `C:\Users\<you>\.gemini\config\hooks.json`.
2. Add the hook command to the event `PreInvocation`. This example is for
   macOS. On Linux, use `/home/<you>` in the paths.

   ```json
   {
     "mai": {
       "PreInvocation": [
         {
           "type": "command",
           "command": "/Users/<you>/.mai/bin/mai hook --credential-file /Users/<you>/.mai/antigravity.lane-a.cred --format antigravity --event PreInvocation",
           "timeout": 10
         }
       ]
     }
   }
   ```

3. On Windows, use this command:

   ```json
   "command": "C:\\Users\\<you>\\.mai\\bin\\mai.cmd hook --credential-file C:\\Users\\<you>\\.mai\\antigravity.lane-a.cred --format antigravity --event PreInvocation"
   ```

4. Restart Antigravity.

If your version of Antigravity has no hooks, use `watch` or the tool
`wait_for_mail`. Refer to [Wait for mail](#wait-for-mail).

## Wait for mail

The command `watch` stops when new mail arrives. An agent can run it as a
background command.

- macOS:

  ```sh
  /Users/<you>/.mai/bin/mai watch --credential-file /Users/<you>/.mai/<agent>.<label>.cred
  ```

- Linux:

  ```sh
  /home/<you>/.mai/bin/mai watch --credential-file /home/<you>/.mai/<agent>.<label>.cred
  ```

- Windows (PowerShell):

  ```powershell
  C:\Users\<you>\.mai\bin\mai.cmd watch --credential-file C:\Users\<you>\.mai\<agent>.<label>.cred
  ```

Refer to [CLI](../reference/cli.md) for the exit codes.

## HTTP

A script can use Streamable HTTP at `POST http://127.0.0.1:8765/mcp`. Each
tool call must have the argument `session`, or the header `Mai-Session`.
The HTTP address is the same on all hosts.

## Test the connection

1. Tell the agent to call `login`.
2. Tell the agent to call `whoami`.
3. Make sure that the result shows the correct login.
