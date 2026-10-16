param([switch]$Race)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
Import-Stage8Env
$oldIntegration = $env:RUN_DB_INTEGRATION
$env:RUN_DB_INTEGRATION = '1'
Push-Location $Stage8Root
try {
    Write-Host "Testes sem cache com PostgreSQL real (race=$Race)..." -ForegroundColor Cyan
    $testArgs = @('test','-v','-count=1','-p','1')
    if ($Race) {
        if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { throw 'Race requires CGO and gcc on PATH. Install a Go-compatible C toolchain or run without -Race.' }
        $testArgs += '-race'
    }
    $testArgs += './...'
    $evidenceDir = Join-Path $Stage8Root '.stage8-evidence'
    New-Item -ItemType Directory -Force $evidenceDir | Out-Null
    & go @testArgs 2>&1 | Tee-Object -FilePath (Join-Path $evidenceDir 'go-tests.txt')
    if ($LASTEXITCODE -ne 0) { throw 'STAGE 8 DB/RACE FAILED' }
    Write-Host "PASS: STAGE 8 DB (race=$Race)" -ForegroundColor Green
} finally { $env:RUN_DB_INTEGRATION = $oldIntegration; Pop-Location }
