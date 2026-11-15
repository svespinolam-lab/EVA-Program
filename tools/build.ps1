# =====================================================================
#  EVA · build script (Windows PowerShell)
#  ---------------------------------------------------------------------
#  Compiles EVA Station into ONE executable (server + SQLite + PC program
#  + phone app embedded) and the single-file phone app.
#
#    powershell -ExecutionPolicy Bypass -File tools\build.ps1
#
#  Output:  dist\eva-station.exe      (Windows, no other files needed)
#           dist\eva-station-linux    (optional, with -Linux)
#           dist\eva-app.html         (phone app in one file, needs Node)
#  Requires Go 1.26 or newer (https://go.dev/dl/). Node is optional.
# =====================================================================
param([switch]$Linux)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
  $local = Join-Path $env:LOCALAPPDATA "eva-tools\go\bin\go.exe"
  if (Test-Path $local) { $go = $local } else { throw "Go is not installed. Download it from https://go.dev/dl/ (1.26 or newer)." }
} else { $go = $go.Source }

New-Item -ItemType Directory -Force dist | Out-Null
Write-Host "Checking code (go vet)..."
& $go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }

Write-Host "Compiling dist\eva-station.exe ..."
$env:CGO_ENABLED = "0"
& $go build -trimpath -ldflags "-s -w" -o dist\eva-station.exe ./cmd/eva-station
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

if ($Linux) {
  Write-Host "Compiling dist\eva-station-linux ..."
  $env:GOOS = "linux"; $env:GOARCH = "amd64"
  & $go build -trimpath -ldflags "-s -w" -o dist\eva-station-linux ./cmd/eva-station
  Remove-Item Env:GOOS, Env:GOARCH
}

if (Get-Command node -ErrorAction SilentlyContinue) {
  Write-Host "Bundling dist\eva-app.html ..."
  node tools\bundle.mjs
}
Write-Host ""
Write-Host "Done. Run:  dist\eva-station.exe   then open http://localhost:8080"
