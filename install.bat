@echo off

REM Compile the Go code
go build -o artemis.exe

REM Move the binary to a directory in PATH
set "install_dir=C:\Program Files\Artemis"
set "install_path=%install_dir%\artemis.exe"

if exist "%install_path%" (
    echo Error: File already exists at %install_path%
    exit /b 1
)

if not exist "%install_dir%" mkdir "%install_dir%"
move artemis.exe "%install_dir%"

echo Artemis has been installed successfully to %install_path%
