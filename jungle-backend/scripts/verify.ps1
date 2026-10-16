param([switch]$Race)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
Import-Stage8Env
$instances = @(Get-Stage8Instances)
foreach ($port in @(Get-Stage8Ports)) {
    $entry = @($instances | Where-Object { [int]$_.port -eq $port })
    if ($entry.Count -ne 1 -or -not (Test-Stage8OwnedProcess $entry[0])) {
        throw 'Complete scripts/bootstrap.ps1 -Start successfully before verification.'
    }
    Wait-Stage8Ready $port
}
$evidence = Join-Path $root '.stage8-evidence'
New-Item -ItemType Directory -Force $evidence | Out-Null
Start-Transcript -Path (Join-Path $evidence 'verification.txt') -Force | Out-Null
Push-Location $root
try {
    & (Join-Path $PSScriptRoot 'test-helpers.ps1')
    & (Join-Path $PSScriptRoot 'test-stage8-db.ps1') -Race:$Race
    & (Join-Path $PSScriptRoot 'test-stage6.ps1')
    & (Join-Path $PSScriptRoot 'test-stage7.ps1')
    & (Join-Path $PSScriptRoot 'test-stage7-recovery.ps1')
    & (Join-Path $PSScriptRoot 'test-stage8.ps1')
    & (Join-Path $PSScriptRoot 'test-stage8-failover.ps1')
    & (Join-Path $PSScriptRoot 'check-stage8.ps1')
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed.' }
    Write-Host 'PASS: go vet static analysis.' -ForegroundColor Green
} finally {
    Pop-Location
    Stop-Transcript | Out-Null
}
