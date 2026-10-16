# Inject a reversible LocalStack outage. The PostgreSQL financial transaction
# commits first; the outbox must eventually publish when LocalStack unpauses.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Get-Content (Join-Path $root '.env') | ForEach-Object {
 $s=$_.Trim();if(-not $s -or $s.StartsWith('#')){return}
 $p=$s.Split('=',2);if($p.Count -eq 2){[Environment]::SetEnvironmentVariable($p[0].Trim(),$p[1].Trim().Trim('"').Trim("'"),'Process')}
}
function Token([string]$id,[string]$secret) {
 (Invoke-RestMethod -Method Post -Uri "$($env:OIDC_ISSUER)/protocol/openid-connect/token" -ContentType 'application/x-www-form-urlencoded' -Body @{grant_type='client_credentials';client_id=$id;client_secret=$secret}).access_token
}
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
$base = "http://127.0.0.1:$((Get-Stage8Ports)[0])"
$internal=Token 'jungle-internal' $env:KEYCLOAK_INTERNAL_SECRET
$alpha=Token 'jungle-provider-alpha' $env:KEYCLOAK_PROVIDER_ALPHA_SECRET
$tag=[guid]::NewGuid().ToString('N')
$player="outage-$tag"
$open=Invoke-RestMethod -Method Post -Uri "$base/internal/dev/wallets/open" -Headers @{Authorization="Bearer $internal"} -ContentType 'application/json' -Body (@{playerId=$player;initialBalance=@{amount='100.00';currency='BRL'}} | ConvertTo-Json -Depth 5)
$cmd=@{externalTransactionId="outage-bet-$tag";playerId=$player;walletId=$open.id;roundId="outage-round-$tag";gameId='outage-game';kind='BET';money=@{amount='20.00';currency='BRL'}}
Write-Host 'Temporarily pausing LocalStack (no containers/volumes are removed).' -ForegroundColor Yellow
$paused=$false
try {
 docker pause $env:LOCALSTACK_CONTAINER | Out-Null
 if($LASTEXITCODE -ne 0){throw 'Could not pause jungle-localstack'}
 $paused=$true
 $response=Invoke-RestMethod -Method Post -Uri "$base/v1/transactions" -Headers @{Authorization="Bearer $alpha";'Idempotency-Key'="outage-key-$tag"} -ContentType 'application/json' -Body ($cmd | ConvertTo-Json -Depth 7)
 if($response.status -ne 'PROCESSED' -or $response.balance.amount -ne '80.00') {throw 'Financial commit failed while LocalStack unavailable'}
 Write-Host 'PASS: Finance committed BRL80.00 while SQS was unavailable' -ForegroundColor Green
 $sql="SELECT count(*) FROM sqs_outbox WHERE aggregate_id='$($open.id)' AND state<>'PUBLISHED'"
 $queued=((docker exec $env:POSTGRES_CONTAINER psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -t -A -c $sql) | Out-String).Trim()
 if([int]$queued -lt 1){throw 'Expected durable unpublished outbox entry'}
 Write-Host 'PASS: PostgreSQL retained events for retry' -ForegroundColor Green
} finally {
 if($paused){docker unpause $env:LOCALSTACK_CONTAINER | Out-Null; if($LASTEXITCODE -ne 0){throw 'Failed to restore LocalStack; run docker unpause for LOCALSTACK_CONTAINER.'}}
}
Write-Host 'LocalStack restored; waiting for asynchronous publisher recovery...' -ForegroundColor Cyan
$published=$false
for($i=0;$i -lt 100;$i++) {
 Start-Sleep -Seconds 2
 $sql="SELECT count(*) FROM sqs_outbox WHERE aggregate_id='$($open.id)' AND state='PUBLISHED'"
 $n=((docker exec $env:POSTGRES_CONTAINER psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -t -A -c $sql) | Out-String).Trim()
 if($n -eq '2'){$published=$true;break}
}
if(-not $published){throw 'Outbox not fully published after LocalStack recovery (inspect logs/queues)'}
Write-Host 'STAGE 7 OUTAGE RECOVERY PASSED: financial commit persisted and outbox published after SQS return.' -ForegroundColor Green
