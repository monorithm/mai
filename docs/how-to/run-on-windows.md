# Run mai on Windows

Use this procedure to run mai on Windows 10 or Windows 11. The container is
a Linux container. On the host, the binary `mai.exe` operates the container
through the Engine API of Docker Desktop.

## Install Docker Desktop

1. Install WSL 2. In PowerShell as an administrator, run this command:

   ```powershell
   wsl --install
   ```

2. Restart the computer.
3. Install Docker Desktop.
4. In Docker Desktop, open Settings, then General.
5. Make sure that "Use the WSL 2 based engine" is on.
6. Make sure that Docker Desktop uses Linux containers.

## Install the CLI

1. Install the CLI with Go 1.27 or a newer version:

   ```powershell
   go install github.com/monorithm/mai/cli/v3/cmd/mai@latest
   ```

   Go puts `mai.exe` in `%USERPROFILE%\go\bin`. Make sure that this folder is
   in your `PATH`. The Windows build uses no C compiler.

2. In a new PowerShell window, make sure that the CLI operates:

   ```powershell
   mai version
   ```

## Start mai

1. Start mai:

   ```powershell
   mai start
   ```

   If Docker Desktop is stopped, the CLI starts it. The CLI pulls the image,
   makes the container, and waits until the container is healthy.

2. Add an agent and connect an app. Refer to
   [Connect an agent](connect-an-agent.md).

## The data

The volumes `mai3-data` and `mai3-spool` hold the data. To keep `/data` in a
folder on the host, use `mai start --data-dir PATH`. The Postfix queue stays
on the volume `mai3-spool`, because a host folder on Windows can hold no Unix
socket.

## Troubleshooting

| Problem | Step |
|---|---|
| `mai start` cannot find the engine | Start Docker Desktop. Then run `mai doctor`. |
| A different program uses the port `8765` | Stop the program that uses the port. Then run `mai start` again. |
| No Windows Hello prompt | Run `mai doctor`. It examines the presence check. |
