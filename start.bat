@echo off
chcp 65001 >nul 2>&1
rem Counterweight Windows launcher. Arguments are forwarded unchanged.
rem The restart loop lives in the binary: counterweight.exe -supervise
rem (exit 0 stops, exit 75 applies an update and restarts, other exits back off).

setlocal
cd /d "%~dp0"

rem Use .\ so cmd finds the binary even when NoDefaultCurrentDirectoryInExePath is set.
set "BIN=.\counterweight.exe"
if not exist "%BIN%" (
	echo [Counterweight] Executable not found: %BIN% 1>&2
	exit /b 1
)

"%BIN%" -supervise %*
exit /b %ERRORLEVEL%
