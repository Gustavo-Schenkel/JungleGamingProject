# Stop instance 8082, verify that 8080+8083 still process concurrently and
# consume/publish SQS, then restart 8082 in a finally block.
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
Import-Stage8Env
$instances = @(Get-Stage8Instances)
if ($instances.Count -ne 3) { throw 'Execute start-stage8.ps1 com tres instancias antes do teste de failover.' }
foreach ($entry in $instances) {
    if (-not (Test-Stage8OwnedProcess $entry)) { throw "Instancia $($entry.port) nao corresponde ao PID registrado." }
}
$ports = @(Get-Stage8Ports)
$failedPort = $ports[1]
& (Join-Path $PSScriptRoot 'stop-stage8.ps1') -Ports @($failedPort)
try {
    Write-Host "Simulando falha da instancia $failedPort; as outras duas continuam ativas." -ForegroundColor Cyan
    Push-Location $Stage8Root
    try {
        $urls = (@($ports[0],$ports[2]) | ForEach-Object { "http://127.0.0.1:$_" }) -join ','
        go run ./cmd/stage8-verify -mode failover -urls $urls -out '.stage8-evidence/stage8-failover.json'
        if ($LASTEXITCODE -ne 0) { throw 'Failover verification FAILED' }
    } finally { Pop-Location }
} finally {
    Write-Host "Restaurando instancia $failedPort..." -ForegroundColor Cyan
    & (Join-Path $PSScriptRoot 'start-stage8.ps1') -Ports @($failedPort)
}
Write-Host 'PASS: operacoes continuaram com duas instancias e a terceira foi reiniciada.' -ForegroundColor Green
