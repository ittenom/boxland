@echo off
setlocal

REM Boxland repo launcher for Windows cmd.exe / PowerShell.
REM Runs the Go CLI directly from this checkout so users can type `boxland`
REM from the repo root before installing a compiled binary.

pushd "%~dp0server" >nul
go run .\cmd\boxland %*
set "code=%ERRORLEVEL%"
popd >nul
exit /b %code%
