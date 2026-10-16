# Smoke + regressao da Etapa 4. Requer API iniciada em 127.0.0.1:8080.
# Funciona em Windows PowerShell 5.1 e PowerShell 7.
# Cria registros de teste novos, identificados por stage4-, sem modificar os antigos.
$ErrorActionPreference = 'Stop'
$base = 'http://127.0.0.1:8080'
$testId = [guid]::NewGuid().ToString('N')
$player = "stage4-$testId"
$provider = 'dev-integration-provider'

function Assert([bool] $condition, [string] $message) {
    if (-not $condition) { throw "FAILED: $message" }
    Write-Host "PASS: $message" -ForegroundColor Green
}
function Send-JSON([string] $path, [object] $body, [string] $key = '') {
    $params = @{ Uri = "$base$path"; Method = 'POST'; ContentType = 'application/json'; Body = ($body | ConvertTo-Json -Depth 10 -Compress) }
    if ($key -ne '') { $params.Headers = @{ 'Idempotency-Key' = $key } }
    return Invoke-RestMethod @params
}
function Assert-HTTP([int] $code, [string] $path, [object] $body, [string] $key, [string] $message) {
    $headers = @{ 'Idempotency-Key' = $key }
    $json = $body | ConvertTo-Json -Depth 10 -Compress
    try {
        $null = Invoke-WebRequest -UseBasicParsing -Uri "$base$path" -Method POST -ContentType 'application/json' -Headers $headers -Body $json
        throw "FAILED: $message (expected HTTP $code, but request succeeded)"
    } catch {
        # WebException (Windows PowerShell 5.1) or HttpResponseException (PS 7).
        $response = $_.Exception.Response
        if ($null -eq $response) { throw }
        Assert ([int]$response.StatusCode -eq $code) $message
    }
}

$health = Invoke-RestMethod "$base/health/ready"
Assert ($health.status -eq 'ready') 'API ready (PostgreSQL connected)'

$wallet = Send-JSON '/internal/dev/wallets/open' @{
    playerId = $player
    initialBalance = @{ amount = '100.00'; currency = 'BRL' }
}
Assert ($wallet.balanceMinorUnits -eq 10000 -and $wallet.version -eq 1) 'OPENING: BRL 100.00, version 1'

$betBody = @{
    providerId = $provider; externalTransactionId = "bet-$testId"; playerId = $player
    walletId = $wallet.id; roundId = "round-$testId"; gameId = 'test-game'
    kind = 'BET'; money = @{ amount = '80.00'; currency = 'BRL' }
}
$betKey = "bet-key-$testId"
$bet = Send-JSON '/internal/dev/wagering/transactions' $betBody $betKey
Assert ($bet.status -eq 'PROCESSED' -and $bet.balance.amount -eq '20.00' -and $bet.walletVersion -eq 2) 'BET: debit BRL 80.00, balance 20.00, version 2'

$replay = Send-JSON '/internal/dev/wagering/transactions' $betBody $betKey
Assert (($replay | ConvertTo-Json -Compress -Depth 10) -ceq ($bet | ConvertTo-Json -Compress -Depth 10)) 'Idempotent replay: exact same response'

$canonicalReplayBody = $betBody.Clone()
$canonicalReplayBody.money = @{ amount = '80.0'; currency = 'BRL' }
$canonicalReplay = Send-JSON '/internal/dev/wagering/transactions' $canonicalReplayBody $betKey
Assert ($canonicalReplay.transactionId -eq $bet.transactionId -and $canonicalReplay.balance.amount -eq '20.00') 'Canonical monetary replay: 80.0 == 80.00'

$otherPayload = $betBody.Clone()
$otherPayload.money = @{ amount = '79.00'; currency = 'BRL' }
Assert-HTTP 409 '/internal/dev/wagering/transactions' $otherPayload $betKey 'Same key with changed payload: HTTP 409'
Assert-HTTP 409 '/internal/dev/wagering/transactions' $betBody "new-key-$testId" 'Same external ID with another key: HTTP 409'

$winBody = @{
    providerId = $provider; externalTransactionId = "win-$testId"; playerId = $player
    walletId = $wallet.id; roundId = "round-$testId"; gameId = 'test-game'
    kind = 'WIN'; money = @{ amount = '30.00'; currency = 'BRL' }
}
$win = Send-JSON '/internal/dev/wagering/transactions' $winBody "win-key-$testId"
Assert ($win.status -eq 'PROCESSED' -and $win.balance.amount -eq '50.00' -and $win.walletVersion -eq 3) 'WIN: credit BRL 30.00, balance 50.00, version 3'

$rejectedBody = @{
    providerId = $provider; externalTransactionId = "reject-$testId"; playerId = $player
    walletId = $wallet.id; roundId = "round-$testId"; gameId = 'test-game'
    kind = 'BET'; money = @{ amount = '80.00'; currency = 'BRL' }
}
Assert-HTTP 422 '/internal/dev/wagering/transactions' $rejectedBody "rejected-key-$testId" 'Insufficient funds: HTTP 422'

$walletAfter = Invoke-RestMethod "$base/internal/wallets/$($wallet.id)"
Assert ($walletAfter.balanceMinorUnits -eq 5000 -and $walletAfter.version -eq 3) 'Persisted balance BRL 50.00 and version 3 (rejected BET unchanged)'

# Decode from RAW HTTP JSON, then explicitly enumerate objects in pipeline.
# Avoid @(Invoke-RestMethod ...): Windows PowerShell may wrap JSON arrays.
$ledgerResponse = Invoke-WebRequest -UseBasicParsing "$base/internal/dev/wallets/$($wallet.id)/ledger"
$ledgerRecords = ConvertFrom-Json -InputObject $ledgerResponse.Content
$ledgerEntries = @($ledgerRecords | ForEach-Object { $_ })
if ($ledgerEntries.Count -ne 3) {
    Write-Host "DEBUG ledger raw JSON: $($ledgerResponse.Content)" -ForegroundColor Yellow
}
Assert ($ledgerEntries.Count -eq 3) 'HTTP ledger: exactly 3 movements'
Assert (@($ledgerEntries | Where-Object { $_.entryType -eq 'OPENING' -and $_.direction -eq 'CREDIT' }).Count -eq 1) 'Ledger OPENING recorded once'
Assert (@($ledgerEntries | Where-Object { $_.entryType -eq 'BET' -and $_.direction -eq 'DEBIT' }).Count -eq 1) 'Ledger BET recorded once'
Assert (@($ledgerEntries | Where-Object { $_.entryType -eq 'WIN' -and $_.direction -eq 'CREDIT' }).Count -eq 1) 'Ledger WIN recorded once'

# Independent database checks, not just an HTTP or PowerShell-array assertion.
$queryCount = "SELECT count(*) FROM ledger_entries WHERE wallet_id='$($wallet.id)'::uuid;"
$dbCount = docker exec jungle-postgres psql -U postgres -d jungle -At -c $queryCount
if ($LASTEXITCODE -ne 0) { throw 'Failed to verify ledger count directly in Docker PostgreSQL' }
Assert ([int](($dbCount | Select-Object -Last 1).Trim()) -eq 3) 'PostgreSQL ledger: exactly 3 persisted movements'

$queryAudit = "SELECT balance_minor_units = ledger_net_minor_units FROM stage4_wallet_audit WHERE wallet_id='$($wallet.id)'::uuid;"
$auditResult = docker exec jungle-postgres psql -U postgres -d jungle -At -c $queryAudit
if ($LASTEXITCODE -ne 0) { throw 'Failed to query audit reconciliation view' }
Assert (($auditResult | Select-Object -Last 1).Trim() -eq 't') 'Reconciliation: wallet balance equals ledger net'

Write-Host "`nSTAGE 4 PASSED. player=$player wallet=$($wallet.id)" -ForegroundColor Green
Write-Host 'The test records remain saved for the final project verification.'
