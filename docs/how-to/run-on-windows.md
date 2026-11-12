# Run mai on Windows

Use this procedure to run mai on Windows 10 or Windows 11. The container is
a Linux container. On the host, the shim `mai.cmd` runs the commands in
the container.

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
7. In a new PowerShell window, make sure that docker operates:

   ```powershell
   docker version
   docker compose version
   ```

## Start the container

A clone of the repository is not necessary. The image
`ghcr.io/monorithm/mai` is public. For a pull, a login and a token are not
necessary. The image has the shims and a compose file.

1. Make the directory for the shims:

   ```powershell
   New-Item -ItemType Directory -Force "$env:USERPROFILE\.mai\bin" | Out-Null
   ```

2. Write the shims and the compose file from the image:

   ```powershell
   docker run --rm -v "$env:USERPROFILE\.mai\bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
   ```

   After this step, the directory has `mai`, `mai.ps1`, `mai.cmd` and `compose.yaml`.

3. Start the container:

   ```powershell
   docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" up -d
   ```

4. Make sure that the container is healthy:

   ```powershell
   & "$env:USERPROFILE\.mai\bin\mai.cmd" health
   ```

The ports on the host are the same as on macOS and Linux. Refer to
[Ports](../reference/ports.md).

In an MCP entry and in a hook command, use the full path of the shim:
`C:\Users\<you>\.mai\bin\mai.cmd`.

## Update mai

1. Pull the new image:

   ```powershell
   docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" pull
   ```

2. Start the container again with the new image:

   ```powershell
   docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" up -d
   ```

3. Write the shims again with the `docker run` command of
   [Start the container](#start-the-container).

## Use the shim

Use `mai.cmd` in cmd, in PowerShell and in the configuration of an app.

- For most commands, `mai.cmd` runs `docker exec -i mai mai` directly. Thus
  the MCP stdio stream goes through only one program.
- For `watch`, `hook` and `session new` with `--save`, `mai.cmd` starts
  `mai.ps1` in the same directory. This script reads and writes the credential files on the
  host.
- `mai.ps1` operates with Windows PowerShell 5.1 and with PowerShell 7. If
  `pwsh` is in `PATH`, `mai.cmd` uses PowerShell 7.

In Git Bash and in WSL, you can also use the sh shim `mai`.

### Execution policy

A change to the execution policy of the computer is not necessary. `mai.cmd`
starts PowerShell with `-ExecutionPolicy Bypass` for its own script only.
This flag has no effect on other scripts. It does not change the policy
of the computer or of the user.

To start `mai.ps1` directly, use the same flag:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File "$env:USERPROFILE\.mai\bin\mai.ps1" health
```

## Make a credential

1. Make the credential directory:

   ```powershell
   New-Item -ItemType Directory -Force "$env:USERPROFILE\.mai" | Out-Null
   ```

2. Make the credential and save it on the host:

   ```powershell
   & "$env:USERPROFILE\.mai\bin\mai.cmd" session new cursor --label lane-a --save "$env:USERPROFILE\.mai\cursor.lane-a.cred"
   ```

The file gets the access rules (ACL) of your user profile. Thus other
standard users cannot read the file. Refer to
[Make a session credential](make-a-session-credential.md).

## Paths with spaces

If your user name has a space, the paths have a space. Put quotes around a
path with a space. In the examples, `<first> <last>` is a user name with a
space.

- In PowerShell, put double quotes around the path. To start a quoted
  path, put the call operator `&` before it:

  ```powershell
  & "C:\Users\<first> <last>\.mai\bin\mai.cmd" health
  ```

- In cmd, put double quotes around the path:

  ```bat
  "C:\Users\<first> <last>\.mai\bin\mai.cmd" health
  ```

- In an MCP entry, the field `command` has only the path. Do not add quotes
  in this field:

  ```json
  {
    "mai": {
      "command": "C:\\Users\\<first> <last>\\.mai\\bin\\mai.cmd",
      "args": ["mcp"]
    }
  }
  ```

- In a hook command, the path and the arguments are in one string. Put
  escaped double quotes around each path with a space:

  ```json
  {
    "command": "\"C:\\Users\\<first> <last>\\.mai\\bin\\mai.cmd\" hook --credential-file \"C:\\Users\\<first> <last>\\.mai\\cursor.lane-a.cred\""
  }
  ```

## Line endings in a clone

A clone is necessary only for development. The file `.gitattributes` of the
repository keeps LF for all text files. Only the files `*.cmd` and `*.ps1`
get CRLF. Thus the configuration templates and the sh shim `bin/mai` stay
correct in a clone on Windows.

1. Make sure that git does not change the line endings of all files:

   ```powershell
   git config --global core.autocrlf false
   ```

   The `.gitattributes` file controls the line endings, also if
   `core.autocrlf` is `true`. The value `false` prevents changes in files
   that `.gitattributes` does not include.

2. Clone the repository into a path without spaces:

   ```powershell
   cd $env:USERPROFILE
   git clone https://github.com/monorithm/mai.git mai
   ```

3. In the clone, build the image. Then start the container:

   ```powershell
   cd mai
   docker compose build
   docker compose up -d
   bin\mai.cmd health
   ```

If you cloned the repository before the `.gitattributes` file was in it,
remove that clone. Then clone the repository again. Before you remove that
clone, push or copy your local changes.

## Troubleshooting

| Problem | Possible cause | Correction |
|---|---|---|
| `docker` is not found. | Docker Desktop is not in `PATH`. | Restart the terminal, or set `MAI_DOCKER` to the full path of `docker.exe`. |
| The container stops with an error in a script. | You built the image from a clone with CRLF line endings in a text file. | Do the steps in [Line endings in a clone](#line-endings-in-a-clone). Then build the image again with `docker compose build`. Or use the image from the registry. |
| A hook shows nothing. | The credential file is not there, or the container is not on. | Make sure that the file is there. Then run `mai.cmd health`. |
| `watch` gives the exit code 2. | The shim cannot read the credential file, or the login was not correct. | Make sure that the path is correct. Then make a new credential. |
