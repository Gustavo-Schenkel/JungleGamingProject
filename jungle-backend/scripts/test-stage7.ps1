# Stage 7 end-to-end: Keycloak auth, inbound SQS, same finance service,
# durable inbox, outbox, FIFO, idempotency and ledger reconciliation.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Get-Content (Join-Path $root '.env') | ForEach-Object {
 $line=$_.Trim();if (-not $line -or $line.StartsWith('#')) {return}
 $p=$line.Split('=',2);if($p.Count -eq 2){[Environment]::SetEnvironmentVariable($p[0].Trim(),$p[1].Trim().Trim('"').Trim("'"),'Process')}
}
function Assert([bool]$ok,[string]$label){if(-not $ok){throw "FAILED: $label"};Write-Host "PASS: $label" -ForegroundColor Green}
function Token([string]$client,[string]$secret) {
 (Invoke-RestMethod -Method Post -Uri "$($env:OIDC_ISSUER)/protocol/openid-connect/token" -ContentType 'application/x-www-form-urlencoded' -Body @{grant_type='client_credentials';client_id=$client;client_secret=$secret}).access_token
}
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
$base = "http://127.0.0.1:$((Get-Stage8Ports)[0])"
$ready=Invoke-RestMethod "$base/health/ready"
Assert ($ready.status -eq 'ready') 'API ready and PostgreSQL accessible'
$internal=Token 'jungle-internal' $env:KEYCLOAK_INTERNAL_SECRET
$alpha=Token 'jungle-provider-alpha' $env:KEYCLOAK_PROVIDER_ALPHA_SECRET
$headers=@{Authorization="Bearer $internal"}
$tag=[guid]::NewGuid().ToString('N')
$player="stage7-player-$tag"
$opening=Invoke-RestMethod -Method Post -Uri "$base/internal/dev/wallets/open" -Headers $headers -ContentType 'application/json' -Body (@{playerId=$player;initialBalance=@{currency='BRL';amount='100.00'}} | ConvertTo-Json -Depth 6)
$wallet=$opening.id
Assert ($opening.balanceMinorUnits -eq 10000) 'Opening BRL100.00 committed'
Push-Location $root
try {
 $external="stage7-bet-$tag"; $key="stage7-key-$tag"
 go run ./cmd/sqs-send -client jungle-provider-alpha -wallet $wallet -player $player -external $external -key $key -round "round-$tag" -kind BET -amount 80.00
 if($LASTEXITCODE -ne 0){throw 'Failed to send BET to SQS'}
 function AwaitResult([string]$id,[string]$expected) {
  for($n=0;$n -lt 40;$n++) {
   Start-Sleep -Seconds 1
   try {
    $r=Invoke-RestMethod -Uri "$base/v1/transactions/$id" -Headers @{Authorization="Bearer $alpha"}
    if($r.status -eq 'PROCESSED' -and $r.balance.amount -eq $expected){return $r}
   } catch { }
  }
  throw "Timeout waiting for SQS financial transaction $id"
 }
 $bet=AwaitResult $external '20.00'
 Assert ($bet.walletVersion -eq 2) 'SQS BET debit BRL80.00 -> BRL20.00 version 2'
 go run ./cmd/sqs-send -client jungle-provider-alpha -wallet $wallet -player $player -external $external -key $key -round "round-$tag" -kind BET -amount 80.00
 if($LASTEXITCODE -ne 0){throw 'Failed to redeliver BET'}
 $winID="stage7-win-$tag"
 go run ./cmd/sqs-send -client jungle-provider-alpha -wallet $wallet -player $player -external $winID -key "stage7-win-key-$tag" -round "round-$tag" -kind WIN -amount 30.00
 if($LASTEXITCODE -ne 0){throw 'Failed to send WIN'}
 $win=AwaitResult $winID '50.00'
 Assert ($win.walletVersion -eq 3) 'SQS WIN credit BRL30.00 -> BRL50.00 version 3'
 # Give the worker time to acknowledge the duplicate message and publish events.
 Start-Sleep -Seconds 3
 $w=Invoke-RestMethod "$base/internal/wallets/$wallet" -Headers $headers
 Assert ($w.balanceMinorUnits -eq 5000 -and $w.version -eq 3) 'Duplicate SQS BET did not move wallet twice'
 $ledger=Invoke-RestMethod "$base/internal/dev/wallets/$wallet/ledger" -Headers $headers
 Assert (@($ledger).Count -eq 3) 'Ledger exactly OPENING, BET, WIN'
 $sql="SELECT (SELECT count(*) FROM sqs_inbox i JOIN wager_transactions t ON t.id=i.transaction_id WHERE t.wallet_id='$wallet') AS inbox,(SELECT count(*) FROM sqs_outbox WHERE aggregate_id='$wallet') AS outbox,(SELECT count(*) FROM sqs_outbox WHERE aggregate_id='$wallet' AND state='PUBLISHED') AS published"
 for($n=0;$n -lt 30;$n++) {
  $counts = docker exec $env:POSTGRES_CONTAINER psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -t -A -c $sql
  $parts=($counts | Out-String).Trim().Split('|')
  if($parts.Count -eq 3 -and [int]$parts[0] -ge 3 -and [int]$parts[1] -eq 3 -and [int]$parts[2] -eq 3){break}
  Start-Sleep -Seconds 1
 }
 Assert ($parts.Count -eq 3 -and [int]$parts[0] -ge 3) 'Inbox durably records new SQS message IDs and replay'
 Assert ($parts.Count -eq 3 -and [int]$parts[1] -eq 3 -and [int]$parts[2] -eq 3) 'Outbox publishes OPENING, BET, WIN after commit'
 $audit="SELECT count(*) FROM stage4_wallet_audit WHERE wallet_id='$wallet' AND balance_minor_units<>ledger_net_minor_units"
 $diff=((docker exec $env:POSTGRES_CONTAINER psql -U $env:POSTGRES_USER -d $env:POSTGRES_DB -t -A -c $audit) | Out-String).Trim()
 Assert ($diff -eq '0') 'No wallet/ledger reconciliation difference'
 Write-Host "STAGE 7 PASSED; wallet=$wallet. Existing project financial data preserved." -ForegroundColor Green
} finally {Pop-Location}
