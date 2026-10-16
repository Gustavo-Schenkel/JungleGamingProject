$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
Import-Stage8Env
$instances = @(Get-Stage8Instances)
if ($instances.Count -ne 3) { throw 'Expected three registered API processes.' }
Write-Host 'Processos independentes:' -ForegroundColor Cyan
foreach ($entry in $instances) {
    $live = Test-Stage8OwnedProcess $entry
    if (-not $live) { throw "API $($entry.port) is not an owned running process." }
    Wait-Stage8Ready ([int]$entry.port)
    Write-Host "port=$($entry.port) PID=$($entry.pid) owned-and-running=$live"
}
$pgContainer = $env:POSTGRES_CONTAINER
if (-not $pgContainer) { $pgContainer = 'jungle-challenge-postgres' }
$pgArgs = @('exec', $pgContainer, 'psql', '-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-U', $env:POSTGRES_USER, '-d', $env:POSTGRES_DB, '-c')
function DbScalar([string]$query) {
    $lines = @(docker @pgArgs $query)
    if ($LASTEXITCODE -ne 0) { throw "SQL failed: $query" }
    return ($lines | Out-String).Trim()
}
$migrations = DbScalar "SELECT count(*) FROM schema_migrations WHERE version IN ('001_initial_schema.sql','002_stage4_financial_operations.sql','003_stage4_audit_guards.sql','004_stage5_reversals.sql','005_stage7_messaging.sql','006_outbox_delivery_order.sql');"
if ($migrations -ne '6') { throw "Expected migrations 001-006; got $migrations" }
$divergences = DbScalar 'SELECT count(*) FROM stage4_wallet_audit WHERE balance_minor_units<>ledger_net_minor_units;'
if ($divergences -ne '0') { throw "Ledger divergence count: $divergences" }
Write-Host 'SQS inbox/outbox:' -ForegroundColor Cyan
docker exec $pgContainer psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -c 'SELECT * FROM stage7_messaging_status;'
if ($LASTEXITCODE -ne 0) { throw 'Outbox view not accessible' }
Write-Host 'PASS: migration history 001-006 and zero wallet/ledger divergence.' -ForegroundColor Green
Write-Host 'Evidence files under .stage8-evidence. Save them for final delivery.' -ForegroundColor Cyan
