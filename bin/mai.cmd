@echo off
rem mai: the host shim of mai (Mail for AI) for Windows: cmd, PowerShell, and apps that start a command.
rem For most commands it runs "docker exec -i mai mai ..." directly, thus the MCP stdio stream does
rem not go through a second program. For watch, hook, and "session new|rotate ... --save" it starts
rem mai.ps1, which reads and writes the credential files on the host. For "mcp" with
rem MAI_CREDENTIAL_FILE set, it gives the text of that host file to the container in MAI_CREDENTIAL.
rem Environment: MAI_CONTAINER (default mai), MAI_DOCKER (default docker), MAI_CREDENTIAL_FILE.
setlocal
if "%MAI_CONTAINER%"=="" (set "MAI_C=mai") else (set "MAI_C=%MAI_CONTAINER%")
if "%MAI_DOCKER%"=="" (set "MAI_D=docker") else (set "MAI_D=%MAI_DOCKER%")
set "MAI_PS=powershell"
where pwsh >nul 2>nul && set "MAI_PS=pwsh"

if /I "%~1"=="watch" goto :ps
if /I "%~1"=="hook" goto :ps
if /I "%~1"=="session" if /I "%~2"=="new" echo %* | findstr /C:"--save" >nul && goto :ps
if /I "%~1"=="session" if /I "%~2"=="rotate" echo %* | findstr /C:"--save" >nul && goto :ps
if /I "%~1"=="mcp" if defined MAI_CREDENTIAL_FILE goto :mcp

"%MAI_D%" exec -i "%MAI_C%" mai %*
exit /b %ERRORLEVEL%

:mcp
rem The text of the host file goes to the container in MAI_CREDENTIAL, not in the arguments of docker.
if not exist "%MAI_CREDENTIAL_FILE%" (echo mai: error: cannot read the credential file 1>&2 & exit /b 2)
set /p MAI_CREDENTIAL=<"%MAI_CREDENTIAL_FILE%"
"%MAI_D%" exec -i -e MAI_CREDENTIAL -e MAI_CLIENT "%MAI_C%" mai %*
exit /b %ERRORLEVEL%

:ps
rem -ExecutionPolicy Bypass applies only to this one script, not to the policy of the computer.
%MAI_PS% -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%~dp0mai.ps1" %*
set "MAI_RC=%ERRORLEVEL%"
if /I "%~1"=="hook" exit /b 0
exit /b %MAI_RC%
