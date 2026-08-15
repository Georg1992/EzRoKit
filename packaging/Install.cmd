@echo off
setlocal
title EzRoKit Setup
cd /d "%~dp0"

set "ERK_INSTALL_DIR=%~dp0"
set "ERK_CMD_PATH=%~f0"
set "ERK_TMPPS1=%TEMP%\ezrokit-%RANDOM%.ps1"
set "ERK_SKIP=0"
for /f "tokens=1 delims=:" %%A in ('findstr /n /b ":PS1" "%~f0"') do set "ERK_SKIP=%%A"

powershell.exe -NoProfile -ExecutionPolicy Bypass -Command ^
  "$skip = [int]$env:ERK_SKIP; $lines = [IO.File]::ReadAllLines($env:ERK_CMD_PATH); $body = ($lines | Select-Object -Skip $skip) -join [Environment]::NewLine; [IO.File]::WriteAllText($env:ERK_TMPPS1, $body, [Text.UTF8Encoding]::new($false))"
if errorlevel 1 goto fail

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%ERK_TMPPS1%"
set ERR=%ERRORLEVEL%
del "%ERK_TMPPS1%" 2>nul
if %ERR% neq 0 goto fail
goto done

:fail
echo.
echo Setup failed. See README.txt
set ERR=1

:done
echo.
pause
exit /b %ERR%

:PS1
$ErrorActionPreference = "Stop"

$InstallDir = $env:ERK_INSTALL_DIR.TrimEnd('\')
$AppDisplayName = "EzRoKit"
$AppExeName = "EzRoKit.exe"
$SourceExe = Join-Path $InstallDir $AppExeName

Write-Host ""
Write-Host "  $AppDisplayName - Setup" -ForegroundColor Cyan
Write-Host ""

if (-not (Test-Path $SourceExe)) {
    Write-Host "Error: Could not find '$AppExeName' in this folder." -ForegroundColor Red
    Write-Host "Extract the full ZIP and run Install.cmd from inside it." -ForegroundColor Yellow
    exit 1
}

Write-Host "Checking input driver..." -ForegroundColor Cyan

$UsbipTargetVersion = [Version]"0.9.7.7"
$UsbipInstalledVersion = $null

$UsbipEntry = Get-ItemProperty "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*" -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -like "USBip version*" } |
    Select-Object -First 1
if (-not $UsbipEntry) {
    $UsbipEntry = Get-ItemProperty "HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*" -ErrorAction SilentlyContinue |
        Where-Object { $_.DisplayName -like "USBip version*" } |
        Select-Object -First 1
}
if ($UsbipEntry) {
    try { $UsbipInstalledVersion = [Version]$UsbipEntry.DisplayVersion } catch { }
}

if (-not $UsbipInstalledVersion) {
    $DriverPath = Join-Path $env:SystemRoot "System32\drivers\usbip2_ude.sys"
    if (Test-Path $DriverPath) {
        try { $UsbipInstalledVersion = [Version](Get-Item $DriverPath).VersionInfo.FileVersion } catch { }
    }
}

$NeedsReboot = $false
$NeedsDriverInstall = $true

if ($UsbipInstalledVersion -and $UsbipInstalledVersion -ge $UsbipTargetVersion) {
    Write-Host "  Input driver OK." -ForegroundColor Green
    $NeedsDriverInstall = $false
}
elseif ($UsbipInstalledVersion) {
    Write-Host "  Updating input driver..." -ForegroundColor Yellow
}
else {
    Write-Host "  Installing input driver..." -ForegroundColor Yellow
}

if ($NeedsDriverInstall) {
    Write-Host ""
    Write-Host "  Click Yes on the Windows security prompt." -ForegroundColor Yellow
    Write-Host ""

    $TempDir = New-TemporaryFile | ForEach-Object { Remove-Item $_; New-Item -ItemType Directory -Path $_ }
    try {
        $UsbipInstallerUrl = "https://github.com/vadimgrn/usbip-win2/releases/download/v.0.9.7.7/USBip-0.9.7.7-x64.exe"
        $UsbipInstaller = Join-Path $TempDir "USBip-setup.exe"
        Invoke-WebRequest -Uri $UsbipInstallerUrl -OutFile $UsbipInstaller -ErrorAction Stop
        Start-Process -FilePath $UsbipInstaller -ArgumentList "/S" -Verb RunAs -Wait
        Write-Host "  Input driver installed." -ForegroundColor Green
        $NeedsReboot = $true
    }
    catch {
        Write-Host "  Could not install the input driver automatically." -ForegroundColor Red
        Write-Host "  $($_.Exception.Message)" -ForegroundColor Red
        Write-Host ""
        Write-Host "  Install manually from:" -ForegroundColor Yellow
        Write-Host "  https://github.com/vadimgrn/usbip-win2/releases" -ForegroundColor Yellow
        Write-Host "  Then restart your PC and run Install.cmd again." -ForegroundColor Yellow
    }
    finally {
        Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
    }
}

Write-Host ""
Write-Host "Setup complete!" -ForegroundColor Green
Write-Host ""
Write-Host "Next:" -ForegroundColor Cyan
Write-Host "  1. Restart your PC if you were asked to (first-time setup only)"
Write-Host "  2. Double-click EzRoKit.exe in this folder"
Write-Host "  3. Click Start, add a trigger key, then open your game"
Write-Host ""

if ($NeedsReboot) {
    Write-Host "Restart your computer before using the clicker." -ForegroundColor Yellow
    Write-Host ""
}

Write-Host "See README.txt for details." -ForegroundColor Gray
Write-Host ""
