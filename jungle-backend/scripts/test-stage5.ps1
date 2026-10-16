# Stage 5 smoke tests; requires the Go API and Docker PostgreSQL.
# Compatible with Windows PowerShell 5.1. Creates fresh stage5-* audit records.
$ErrorActionPreference = 'Stop'
$base = 'http://127.0.0.1:8080'
$id = [guid]::NewGuid().ToString('N')
$provider = 'dev-stage5-provider'
$round = "round-$id"
$game = 'stage5-test-game'
$path = '/internal/dev/wagering/transactions'

function Assert([bool]$ok,[string]$message) {
    if (-not $ok) { throw "FAIL: $message" }
    Write-Host "PASS: $message" -ForegroundColor Green
}
function Request-JSON([string]$uri,[object]$payload,[string]$key = '') {
    $params = @{Uri="$base$uri";Method='POST';ContentType='application/json';Body=($payload | ConvertTo-Json -Depth 10 -Compress)}
    if ($key) {$params.Headers=@{'Idempotency-Key'=$key}}
    return Invoke-RestMethod @params
}
function New-Wallet([string]$name,[string]$opening) {
    return Request-JSON '/internal/dev/wallets/open' @{playerId=$name;initialBalance=@{amount=$opening;currency='BRL'}}
}
function New-Command([object]$w,[string]$kind,[string]$external,[string]$amount,[string]$reference='') {
    $cmd=@{providerId=$provider;externalTransactionId=$external;playerId=$w.playerId;walletId=$w.id;roundId=$round;gameId=$game;kind=$kind;money=@{amount=$amount;currency='BRL'}}
    if ($reference) {$cmd.referenceExternalTransactionId=$reference}
    return $cmd
}
function Send-Transaction([object]$cmd) {
    return Request-JSON $path $cmd ("key-"+$cmd.externalTransactionId)
}
function Expect-422([object]$cmd,[string]$expectedFailure) {
    try {
        $null=Invoke-WebRequest -UseBasicParsing -Uri "$base$path" -Method POST -ContentType 'application/json' -Headers @{'Idempotency-Key'=("key-"+$cmd.externalTransactionId)} -Body ($cmd | ConvertTo-Json -Depth 10 -Compress)
        throw "Expected HTTP 422 for $($cmd.externalTransactionId)"
    } catch {
        if ($null -eq $_.Exception.Response -or [int]$_.Exception.Response.StatusCode -ne 422) {throw}
    }
    $sql="SELECT failure_code FROM wager_transactions WHERE provider_id='$provider' AND external_transaction_id='$($cmd.externalTransactionId)';"
    $out=docker exec jungle-postgres psql -U postgres -d jungle -At -c $sql
    if ($LASTEXITCODE -ne 0) {throw 'Database verification failed'}
    Assert (($out | Select-Object -Last 1).Trim() -eq $expectedFailure) "REJECTED: $expectedFailure"
}
function Check-Audit([object]$w,[int]$expectedEntries) {
    $sql="SELECT ledger_entry_count, (balance_minor_units=ledger_net_minor_units) FROM stage4_wallet_audit WHERE wallet_id='$($w.id)'::uuid;"
    $rows=docker exec jungle-postgres psql -U postgres -d jungle -At -c $sql
    if ($LASTEXITCODE -ne 0) {throw 'Could not inspect ledger reconciliation'}
    $cells=(($rows | Select-Object -Last 1).Trim()).Split('|')
    Assert ([int]$cells[0] -eq $expectedEntries -and $cells[1] -eq 't') "Ledger: $expectedEntries movements and consistent balance"
}

$health=Invoke-RestMethod "$base/health/ready"
Assert ($health.status -eq 'ready') 'API ready and PostgreSQL connected'
$w=New-Wallet "stage5-main-$id" '100.00'
Assert ($w.balanceMinorUnits -eq 10000 -and $w.version -eq 1) 'OPENING 100.00 version 1'
$betExternal="bet-$id"
$bet=Send-Transaction (New-Command $w 'BET' $betExternal '40.00')
Assert ($bet.status -eq 'PROCESSED' -and $bet.balance.amount -eq '60.00' -and $bet.walletVersion -eq 2) 'BET 40.00 -> balance 60.00'
$refundExternal="refund-$id"
$refundCommand=New-Command $w 'REFUND' $refundExternal '40.00' $betExternal
$refund=Send-Transaction $refundCommand
Assert ($refund.status -eq 'PROCESSED' -and $refund.balance.amount -eq '100.00' -and $refund.walletVersion -eq 3) 'REFUND 40.00 -> balance 100.00'
$replay=Send-Transaction $refundCommand
Assert ($replay.transactionId -eq $refund.transactionId -and $replay.walletVersion -eq $refund.walletVersion) 'REFUND replay is idempotent'
Expect-422 (New-Command $w 'REFUND' "duplicate-refund-$id" '40.00' $betExternal) 'ALREADY_REVERSED'
$loss=Send-Transaction (New-Command $w 'LOSS' "loss-$id" '0.00')
Assert ($loss.status -eq 'PROCESSED' -and $loss.balance.amount -eq '100.00' -and $loss.walletVersion -eq 3) 'LOSS: no balance/ledger/version movement'
$reverseRefund=Send-Transaction (New-Command $w 'ROLLBACK' "rb-refund-$id" '40.00' $refundExternal)
Assert ($reverseRefund.status -eq 'PROCESSED' -and $reverseRefund.balance.amount -eq '60.00' -and $reverseRefund.walletVersion -eq 4) 'ROLLBACK REFUND -> debit 40.00'
$winExternal="win-$id"
$win=Send-Transaction (New-Command $w 'WIN' $winExternal '30.00')
Assert ($win.status -eq 'PROCESSED' -and $win.balance.amount -eq '90.00' -and $win.walletVersion -eq 5) 'WIN 30.00'
$reverseWin=Send-Transaction (New-Command $w 'ROLLBACK' "rb-win-$id" '30.00' $winExternal)
Assert ($reverseWin.status -eq 'PROCESSED' -and $reverseWin.balance.amount -eq '60.00' -and $reverseWin.walletVersion -eq 6) 'ROLLBACK WIN -> debit 30.00'
Expect-422 (New-Command $w 'ROLLBACK' "duplicate-rb-win-$id" '30.00' $winExternal) 'ALREADY_REVERSED'
$stored=Invoke-RestMethod "$base/internal/wallets/$($w.id)"
Assert ($stored.balanceMinorUnits -eq 6000 -and $stored.version -eq 6) 'Persisted balance 60.00, version 6'
Check-Audit $w 6

# Explicitly test that a rollback cannot make the wallet negative.
$poor=New-Wallet "stage5-poor-$id" '0.00'
$poorWin="poor-win-$id"
$null=Send-Transaction (New-Command $poor 'WIN' $poorWin '20.00')
$null=Send-Transaction (New-Command $poor 'BET' "poor-bet-$id" '20.00')
Expect-422 (New-Command $poor 'ROLLBACK' "poor-rb-$id" '20.00' $poorWin) 'REVERSAL_INSUFFICIENT_FUNDS'
Check-Audit $poor 2

# Reference may arrive after refund; the worker must resume it durably.
$pendingWallet=New-Wallet "stage5-pending-$id" '100.00'
$futureBet="future-bet-$id"
$pendingCmd=New-Command $pendingWallet 'REFUND' "early-refund-$id" '10.00' $futureBet
$pending=Send-Transaction $pendingCmd
Assert ($pending.status -eq 'PENDING_REFERENCE' -and $pending.walletVersion -eq 1) 'REFUND before BET persisted as PENDING_REFERENCE'
$null=Send-Transaction (New-Command $pendingWallet 'BET' $futureBet '10.00')
$resumed=$false
for ($i=0; $i -lt 30; $i++) {
    Start-Sleep -Milliseconds 500
    $sql="SELECT status FROM wager_transactions WHERE id='$($pending.transactionId)'::uuid;"
    $state=docker exec jungle-postgres psql -U postgres -d jungle -At -c $sql
    if ($LASTEXITCODE -ne 0) {throw 'Unable to read reference state'}
    if (($state | Select-Object -Last 1).Trim() -eq 'PROCESSED') {$resumed=$true;break}
}
Assert $resumed 'Worker recovered the reference and processed REFUND'
$recovered=Send-Transaction $pendingCmd
Assert ($recovered.status -eq 'PROCESSED' -and $recovered.transactionId -eq $pending.transactionId -and $recovered.balance.amount -eq '100.00' -and $recovered.walletVersion -eq 3) 'Terminal replay of recovered pending REFUND'
Check-Audit $pendingWallet 3

Write-Host "`nSTAGE 5 PASSED. Audit player=$($w.playerId) wallet=$($w.id)" -ForegroundColor Green
Write-Host 'Stage 4 entries and all previous data remain in PostgreSQL.'
