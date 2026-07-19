# Build the Vue SPA, then the Go binary with the embedded dist (Windows).
# Usage:  .\build.ps1      then   .\fragments.exe serve
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    Write-Host '==> Building frontend (web/)' -ForegroundColor Cyan
    Push-Location web
    npm install
    npm run build
    # vite empties dist/ — restore the committed placeholder go:embed relies on
    New-Item -ItemType File -Force dist/.gitkeep | Out-Null
    Pop-Location

    Write-Host '==> Building Go binary (fragments.exe)' -ForegroundColor Cyan
    $env:CGO_ENABLED = '0'
    go build -o fragments.exe ./cmd/fragments

    Write-Host 'Done. Run:  $env:FRAGMENTS_PASSWORD="..."; .\fragments.exe serve' -ForegroundColor Green
}
finally {
    Pop-Location
}
