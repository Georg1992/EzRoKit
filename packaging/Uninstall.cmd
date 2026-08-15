@echo off
setlocal
title EzRoKit Uninstall
cd /d "%~dp0"

set "ERK_INSTALL_DIR=%~dp0"
set "ERK_CMD_PATH=%~f0"
set "ERK_TMPPS1=%TEMP%\ezrokit-uninstall-%RANDOM%.ps1"
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
echo Uninstall failed. See README.txt
set ERR=1

:done
echo.
pause
exit /b %ERR%

:PS1
$ErrorActionPreference = "Continue"

$AppDisplayName = "EzRoKit"

Write-Host ""
Write-Host "  $AppDisplayName - Uninstall" -ForegroundColor Cyan
Write-Host ""

Write-Host "Stopping clicker..." -ForegroundColor Cyan
Stop-Process -Name "EzRoKit" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "app" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "Belarus Champ Tools" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "clicker" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "viiper" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "wusbip" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "unins000" -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
Write-Host "  Done." -ForegroundColor Green

Write-Host ""
Write-Host "Removing shortcuts and app data (if any)..." -ForegroundColor Cyan

$appDataDir = Join-Path $env:LOCALAPPDATA "EzRoKit"
if (Test-Path $appDataDir) {
    Remove-Item $appDataDir -Recurse -Force
    Write-Host "  Removed $appDataDir" -ForegroundColor Green
}
# Legacy cleanup: remove app data from the pre-rename "Belarus Champ Tools" installs.
$legacyDir = Join-Path $env:LOCALAPPDATA "BelarusChampTools"
if (Test-Path $legacyDir) {
    Remove-Item $legacyDir -Recurse -Force
    Write-Host "  Removed $legacyDir" -ForegroundColor Green
}

$shortcutNames = @(
    "$AppDisplayName.lnk",
    "BelarusChampTools.lnk",
    "clicker.lnk",
    "USBip.lnk",
    "Uninstall USBip.lnk"
)

$shortcutDirs = @(
    [Environment]::GetFolderPath("Desktop"),
    [Environment]::GetFolderPath("CommonDesktopDirectory"),
    (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"),
    [Environment]::GetFolderPath("CommonPrograms")
) | Select-Object -Unique

foreach ($dir in $shortcutDirs) {
    if (-not (Test-Path $dir)) { continue }
    foreach ($name in $shortcutNames) {
        $shortcutPath = Join-Path $dir $name
        if (Test-Path $shortcutPath) {
            Remove-Item $shortcutPath -Force
            Write-Host "  Removed $shortcutPath" -ForegroundColor Green
        }
    }
}

foreach ($menuDir in @(
    (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\USBip"),
    (Join-Path ([Environment]::GetFolderPath("CommonPrograms")) "USBip")
)) {
    if (Test-Path $menuDir) {
        Remove-Item $menuDir -Recurse -Force -ErrorAction SilentlyContinue
        Write-Host "  Removed $menuDir" -ForegroundColor Green
    }
}

$shell = New-Object -ComObject WScript.Shell
foreach ($dir in $shortcutDirs) {
    if (-not (Test-Path $dir)) { continue }
    Get-ChildItem -Path $dir -Filter "*.lnk" -File -ErrorAction SilentlyContinue | ForEach-Object {
        try {
            $target = $shell.CreateShortcut($_.FullName).TargetPath
        } catch {
            return
        }
        if ($target -like "*EzRoKit.exe" -or $target -like "*\EzRoKit\*" -or $target -like "*Belarus Champ Tools.exe" -or $target -like "*\BelarusChampTools\*" -or $target -like "*\USBip\*") {
            Remove-Item $_.FullName -Force
            Write-Host "  Removed $($_.FullName)" -ForegroundColor Green
        }
    }
}

foreach ($startMenuRoot in @(
    (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"),
    ([Environment]::GetFolderPath("CommonPrograms"))
)) {
    if (-not (Test-Path $startMenuRoot)) { continue }
    Get-ChildItem -Path $startMenuRoot -Filter "*.lnk" -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object {
        try {
            $target = $shell.CreateShortcut($_.FullName).TargetPath
        } catch {
            return
        }
        if ($target -like "*EzRoKit.exe" -or $target -like "*\EzRoKit\*" -or $target -like "*Belarus Champ Tools.exe" -or $target -like "*\BelarusChampTools\*" -or $target -like "*\USBip\*") {
            Remove-Item $_.FullName -Force
            Write-Host "  Removed $($_.FullName)" -ForegroundColor Green
        }
    }
}

Write-Host ""
Write-Host "Removing input driver..." -ForegroundColor Cyan

$UsbipDir = Join-Path ${env:ProgramFiles} "USBip"
$UsbipDriverPath = Join-Path $env:SystemRoot "System32\drivers\usbip2_ude.sys"
$UsbipInstalled = (Test-Path $UsbipDir) -or (Test-Path $UsbipDriverPath)

if (-not $UsbipInstalled) {
    Write-Host "  USBip driver not found (already removed)." -ForegroundColor Gray
} else {
    Write-Host "  Click Yes on the Windows security prompt." -ForegroundColor Yellow

    $driverScript = Join-Path $env:TEMP "ezrokit-usbip-driver-uninstall.ps1"
    $driverLog = Join-Path $env:TEMP "ezrokit-usbip-driver-uninstall.log"
    Remove-Item $driverLog -Force -ErrorAction SilentlyContinue

    $driverBody = @'
$LogFile = Join-Path $env:TEMP "ezrokit-usbip-driver-uninstall.log"
function Write-Log([string]$Message) {
    Add-Content -Path $LogFile -Value $Message -Encoding UTF8
}

Write-Log "USBip driver removal started."

Stop-Process -Name "viiper" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "wusbip" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "unins000" -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

$UsbipDir = Join-Path $env:ProgramFiles "USBip"
$Hwid = "ROOT\USBIP_WIN2\UDE"

$usbipExe = Join-Path $UsbipDir "usbip.exe"
if (Test-Path $usbipExe) {
    Write-Log "Detaching USBip devices..."
    & $usbipExe detach -a 2>&1 | ForEach-Object { Write-Log $_ }
    Write-Log "usbip detach exit code: $LASTEXITCODE"
}

$devnodeExe = Join-Path $UsbipDir "devnode.exe"
if (Test-Path $devnodeExe) {
    Write-Log "Removing virtual device $Hwid..."
    & $devnodeExe remove $Hwid root 2>&1 | ForEach-Object { Write-Log $_ }
    Write-Log "devnode exit code: $LASTEXITCODE"
}

Write-Log "Deleting driver packages..."
$infOutput = cmd /c 'findstr /M /L /Q:u "usbip2_filter usbip2_ude" C:\Windows\INF\oem*.inf' 2>$null
if ($infOutput) {
    foreach ($infPath in ($infOutput -split "`r?`n")) {
        $infPath = $infPath.Trim()
        if (-not $infPath) { continue }
        $infName = Split-Path $infPath -Leaf
        Write-Log "pnputil /delete-driver $infName /uninstall"
        & pnputil.exe /delete-driver $infName /uninstall 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "pnputil exit code: $LASTEXITCODE"
    }
} else {
    Write-Log "No USBip driver INF files found."
}

if (Test-Path $UsbipDir) {
    Write-Log "Removing $UsbipDir..."
    Remove-Item $UsbipDir -Recurse -Force -ErrorAction SilentlyContinue
}

$usbipDesktopShortcut = Join-Path ([Environment]::GetFolderPath("CommonDesktopDirectory")) "USBip.lnk"
if (Test-Path $usbipDesktopShortcut) {
    Write-Log "Removing $usbipDesktopShortcut"
    Remove-Item $usbipDesktopShortcut -Force -ErrorAction SilentlyContinue
}

$usbipStartMenu = Join-Path ([Environment]::GetFolderPath("CommonPrograms")) "USBip"
if (Test-Path $usbipStartMenu) {
    Write-Log "Removing $usbipStartMenu"
    Remove-Item $usbipStartMenu -Recurse -Force -ErrorAction SilentlyContinue
}

foreach ($regRoot in @(
    "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall",
    "HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall"
)) {
    Get-ChildItem $regRoot -ErrorAction SilentlyContinue | ForEach-Object {
        $entry = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
        if ($entry.DisplayName -like "USBip version*") {
            Write-Log "Removing registry key $($_.PSPath)"
            Remove-Item $_.PSPath -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

if (Test-Path $UsbipDir) {
    Write-Log "FAILED: USBip folder still exists."
    exit 1
}

Write-Log "USBip driver removal finished."
exit 0
'@

    [IO.File]::WriteAllText($driverScript, $driverBody, [Text.UTF8Encoding]::new($false))

    $proc = Start-Process powershell.exe -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$driverScript`"" -Verb RunAs -Wait -PassThru
    Remove-Item $driverScript -Force -ErrorAction SilentlyContinue

    if ($proc.ExitCode -ne 0) {
        Write-Host "  Driver removal failed (exit $($proc.ExitCode))." -ForegroundColor Red
        if (Test-Path $driverLog) {
            Write-Host "  Log: $driverLog" -ForegroundColor Yellow
            Get-Content $driverLog | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
        }
        Write-Host "  Restart your PC, then run Uninstall.cmd again or delete C:\Program Files\USBip manually." -ForegroundColor Yellow
        exit 1
    }

    Write-Host "  Input driver removed." -ForegroundColor Green
    Write-Host ""
    Write-Host "Restart your computer to finish removal." -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Uninstall complete." -ForegroundColor Green
Write-Host ""
Write-Host "Delete this folder to remove the app:" -ForegroundColor Cyan
$folder = $env:ERK_INSTALL_DIR.TrimEnd('\')
Write-Host "  $folder" -ForegroundColor Gray
Write-Host ""
Write-Host "See README.txt for details." -ForegroundColor Gray
Write-Host ""
