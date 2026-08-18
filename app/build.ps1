$ErrorActionPreference = "Stop"

$appDir = $PSScriptRoot
$rootDir = Split-Path $appDir -Parent
$viiperOut = Join-Path $rootDir "VIIPER\dist\viiper.exe"
$embedDir = Join-Path $appDir "gui\embed"
$embedExe = Join-Path $embedDir "viiper.exe"
$appOut = Join-Path $rootDir "app.exe"
$appBuildOut = Join-Path $rootDir "app.exe.new"

function Remove-BrokenAppOutputs {
    param([string]$Root)

    Stop-Process -Name app -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 200

    $patterns = @(
        "app.exe~",
        "app.exe.new",
        "app.exe.bak",
        "app.exe.old",
        "app-*.exe"
    )

    foreach ($pattern in $patterns) {
        Remove-Item (Join-Path $Root $pattern) -Force -ErrorAction SilentlyContinue
    }

    Get-ChildItem -Path $Root -Force -File -ErrorAction SilentlyContinue |
        Where-Object {
            $_.Extension -eq '.exe' -and
            $_.Name -like 'app*' -and
            $_.Name -ne 'app.exe'
        } |
        Remove-Item -Force -ErrorAction SilentlyContinue
}

Remove-BrokenAppOutputs -Root $rootDir

New-Item -ItemType Directory -Force $embedDir | Out-Null

Write-Host "Building viiper.exe..." -ForegroundColor Cyan
Push-Location (Join-Path $rootDir "VIIPER")
New-Item -ItemType Directory -Force "dist" | Out-Null
$env:CGO_ENABLED = "0"
go build -trimpath -o $viiperOut .\cmd\viiper
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

Copy-Item $viiperOut $embedExe -Force

Push-Location $appDir

Write-Host "Downloading Go modules..." -ForegroundColor Cyan
go mod download
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }

Write-Host "Generating GUI manifest..." -ForegroundColor Cyan
Remove-Item "$appDir\gui\*.syso" -Force -ErrorAction SilentlyContinue
Push-Location (Join-Path $appDir "gui")
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -o rsrc.syso
if ($LASTEXITCODE -ne 0) { Pop-Location; Pop-Location; exit $LASTEXITCODE }
Pop-Location

Write-Host "Building app.exe..." -ForegroundColor Cyan
Remove-Item $appBuildOut -Force -ErrorAction SilentlyContinue
go build -trimpath -ldflags="-H windowsgui" -o $appBuildOut .\gui
if ($LASTEXITCODE -ne 0) {
    Remove-Item $appBuildOut -Force -ErrorAction SilentlyContinue
    Pop-Location
    exit $LASTEXITCODE
}

Remove-Item $appOut -Force -ErrorAction SilentlyContinue
Move-Item $appBuildOut $appOut -Force

Copy-Item (Join-Path $appDir "gui\app.manifest") (Join-Path $rootDir "app.exe.manifest") -Force

Pop-Location

Remove-BrokenAppOutputs -Root $rootDir

Write-Host ""
Write-Host "Done. Run:" -ForegroundColor Green
Write-Host "  $appOut" -ForegroundColor Yellow
