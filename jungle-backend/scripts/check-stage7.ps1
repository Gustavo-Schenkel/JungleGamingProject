$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Get-Content (Join-Path $root '.env') | ForEach-Object {
 $s=$_.Trim();if(-not $s -or $s.StartsWith('#')){return}
 $p=$s.Split('=',2);if($p.Count -eq 2){[Environment]::SetEnvironmentVariable($p[0].Trim(),$p[1].Trim().Trim('"').Trim("'"),'Process')}
}
Write-Host 'PostgreSQL Docker e migracoes:' -ForegroundColor Cyan
docker ps --filter name=jungle-postgres
if($LASTEXITCODE -ne 0){throw 'PostgreSQL unavailable'}
docker exec jungle-postgres psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -c 'SELECT version,applied_at FROM schema_migrations ORDER BY version;'
if($LASTEXITCODE -ne 0){throw 'Migrations query failed'}
Write-Host 'SQS inbox/outbox snapshot:' -ForegroundColor Cyan
docker exec jungle-postgres psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -c 'SELECT * FROM stage7_messaging_status;'
if($LASTEXITCODE -ne 0){throw 'Stage 7 migration missing'}
Write-Host 'Somente divergencias de ledger (esperado 0 rows):' -ForegroundColor Cyan
docker exec jungle-postgres psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -c 'SELECT wallet_id,player_id,balance_minor_units,ledger_net_minor_units FROM stage4_wallet_audit WHERE balance_minor_units <> ledger_net_minor_units;'
if($LASTEXITCODE -ne 0){throw 'Ledger reconciliation failed'}
