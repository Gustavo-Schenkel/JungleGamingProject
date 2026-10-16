# Run PostgreSQL integration tests with race detection in a dedicated shell.
# Requires an already-running Docker PostgreSQL 16 and stage5 migrations.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$file = Join-Path $root '.env'
if (-not (Test-Path $file)) {throw 'Configure .env before running integration tests'}
Get-Content $file | ForEach-Object {
    $line=$_.Trim()
    if (-not $line -or $line.StartsWith('#')) {return}
    $parts=$line.Split('=',2)
    if ($parts.Count -ne 2) {throw 'Invalid .env file'}
    $name=$parts[0].Trim()
    $value=$parts[1].Trim().Trim('"').Trim("'")
    if ($name -match '^[A-Za-z_][A-Za-z0-9_]*$') {
        [Environment]::SetEnvironmentVariable($name,$value,'Process')
    }
}
if ($env:POSTGRES_PORT -ne '5433' -or $env:POSTGRES_HOST -notin @('127.0.0.1','localhost')) {
    throw 'Refusing non-Docker PostgreSQL: expected 127.0.0.1:5433'
}
$env:RUN_DB_INTEGRATION='1'
$env:CGO_ENABLED='1'
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    if (Test-Path 'C:\msys64\ucrt64\bin\gcc.exe') {
        $env:Path = "C:\msys64\ucrt64\bin;$env:Path"
    } else {
        throw 'GCC is not available. Install MSYS2 UCRT64 GCC or add it to PATH.'
    }
}
$env:CC='gcc'
Write-Host 'PostgreSQL integration + race tests (Stage 4 and Stage 5)' -ForegroundColor Cyan
Write-Host 'These tests CREATE new stage-specific wallets and audit entries; no existing data is removed.'
Push-Location $root
try {
    go test -race -count=1 -timeout=180s ./internal/finance
    if ($LASTEXITCODE -ne 0) {throw 'Go integration tests failed'}
} finally {
    Pop-Location
    Remove-Item Env:RUN_DB_INTEGRATION -ErrorAction SilentlyContinue
}
