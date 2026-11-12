# mai: the host shim of mai (Mail for AI) for PowerShell (Windows PowerShell 5.1 and PowerShell 7).
# bin\mai.cmd starts this script for watch, hook and "session new ... --save". The script also works
# on macOS and Linux with pwsh.
#
# It runs the command "mai" in the container "mai". For watch and hook, it reads the credential
# file on the host and gives it to the container on stdin ("--credential-file -"). For hook, the
# hook payload of the app follows the credential line. A hook never fails: on any error it prints
# nothing and exits with code 0.
#
# Environment: MAI_CONTAINER (default mai), MAI_DOCKER (default docker), MAI_CREDENTIAL_FILE.

# Continue: in Windows PowerShell 5.1, Stop makes each stderr line of docker a terminating error.
$ErrorActionPreference = 'Continue'
$script:rc = 0
$container = if ($env:MAI_CONTAINER) { $env:MAI_CONTAINER } else { 'mai' }
$docker = if ($env:MAI_DOCKER) { $env:MAI_DOCKER } else { 'docker' }
$argv = @($args)
$cmd = if ($argv.Count -gt 0) { [string]$argv[0] } else { '' }

function Expand-HomePath([string]$Path) {
    if ($Path -like '~/*' -or $Path -like '~\*') { return Join-Path $HOME $Path.Substring(2) }
    return [Environment]::ExpandEnvironmentVariables($Path)
}

# Find "--name VALUE" or "--name=VALUE" in $List. Give the value and the other arguments.
function Split-Option([object[]]$List, [string]$Name) {
    $value = $null
    $rest = New-Object System.Collections.Generic.List[string]
    for ($i = 0; $i -lt $List.Count; $i++) {
        $a = [string]$List[$i]
        if ($a -eq $Name) {
            if ($i + 1 -lt $List.Count) { $value = [string]$List[$i + 1]; $i++ }
        } elseif ($a.StartsWith("$Name=")) {
            $value = $a.Substring($Name.Length + 1)
        } else {
            $rest.Add($a)
        }
    }
    return @{ Value = $value; Rest = @($rest) }
}

# The output of docker goes to the output of this script. The exit code goes to $script:rc, not to
# the output: a caller that writes "exit (Invoke-Mai ...)" would collect the output of docker.
function Invoke-Mai([string[]]$MaiArgs, $InputText) {
    if ($null -eq $InputText) {
        & $docker exec -i $container mai @MaiArgs
    } else {
        $InputText | & $docker exec -i $container mai @MaiArgs
    }
    $script:rc = $LASTEXITCODE
}

if ($cmd -eq 'session' -and $argv.Count -gt 1 -and ([string]$argv[1] -eq 'new' -or [string]$argv[1] -eq 'rotate')) {
    $split = Split-Option $argv '--save'
    if ($null -eq $split.Value) { Invoke-Mai $split.Rest $null; exit $script:rc }
    $save = Expand-HomePath $split.Value
    $dir = Split-Path -Parent $save
    if ($dir -and -not (Test-Path -LiteralPath $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        # A new folder of credentials is for this user only, as with bin/mai (umask 077).
        if (-not $IsWindows -and $PSVersionTable.PSVersion.Major -ge 6) { chmod 700 $dir }
    }
    $cred = & $docker exec -i $container mai @($split.Rest + '--credential-only')
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    # ASCII and LF: the same bytes as a file that bin/mai writes. On Windows the file gets the ACL of the folder.
    [System.IO.File]::WriteAllText($save, (([string]($cred | Select-Object -Last 1)).Trim() + "`n"), [System.Text.Encoding]::ASCII)
    if (-not $IsWindows -and $PSVersionTable.PSVersion.Major -ge 6) { chmod 600 $save }
    Write-Output "mai: the credential is in $save"
    exit 0
}

if ($cmd -eq 'watch' -or $cmd -eq 'hook') {
    try {
        $split = Split-Option $argv '--credential-file'
        $cred = $split.Value
        if (-not $cred -and $env:MAI_CREDENTIAL_FILE) { $cred = $env:MAI_CREDENTIAL_FILE }
        $rest = @($split.Rest + '--credential-file' + '-')
        if (-not $cred) { throw 'no credential file' }
        $cred = Expand-HomePath $cred
        if (-not (Test-Path -LiteralPath $cred -PathType Leaf)) { throw 'no credential file' }
        $text = [System.IO.File]::ReadAllText($cred)
        if ($cmd -eq 'hook') {
            # The container gets the credential on the first line of stdin, and after it the hook
            # payload of the app (the stdin of this script). A console gives no payload. The script
            # reads the bytes of stdin as UTF-8, and gives UTF-8 with no BOM to docker.
            $line = ($text -split "`r?`n")[0]
            $payload = ''
            if ([Console]::IsInputRedirected) {
                $buffer = New-Object System.IO.MemoryStream
                [Console]::OpenStandardInput().CopyTo($buffer)
                $payload = (New-Object System.Text.UTF8Encoding $false).GetString($buffer.ToArray())
            }
            $OutputEncoding = New-Object System.Text.UTF8Encoding $false
            $hookInput = $line + "`n" + $payload
            $out = $hookInput | & $docker exec -i $container mai @rest 2>$null
            if ($LASTEXITCODE -ne 0) { exit 0 }
            if ($out) { $out | ForEach-Object { Write-Output $_ } }
            exit 0
        }
        Invoke-Mai $rest $text
        exit $script:rc
    } catch {
        if ($cmd -eq 'hook') { exit 0 }
        [Console]::Error.WriteLine('mai: error: cannot read the credential file')
        exit 2
    }
}

if ($cmd -eq 'mcp' -and $env:MAI_CREDENTIAL_FILE) {
    # The text of the host file goes to the container in MAI_CREDENTIAL: the server logs in at start.
    $cred = Expand-HomePath $env:MAI_CREDENTIAL_FILE
    if (-not (Test-Path -LiteralPath $cred -PathType Leaf)) {
        [Console]::Error.WriteLine('mai: error: cannot read the credential file')
        exit 2
    }
    $env:MAI_CREDENTIAL = ([System.IO.File]::ReadAllText($cred)).Trim()
    & $docker exec -i -e MAI_CREDENTIAL -e MAI_CLIENT $container mai @argv
    exit $LASTEXITCODE
}

Invoke-Mai $argv $null
exit $script:rc
