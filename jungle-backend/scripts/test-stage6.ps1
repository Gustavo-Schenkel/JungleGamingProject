# End-to-end Stage 6 test: Keycloak, Bearer JWT, isolated providers, internal-only wallets.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$envFile=Join-Path $root '.env'
if (-not (Test-Path $envFile)) {throw 'Arquivo .env ausente.'}
Get-Content $envFile | ForEach-Object {
 $line=$_.Trim()
 if ($line -eq '' -or $line.StartsWith('#')) {return}
 $parts=$line.Split('=',2)
 if ($parts.Count -eq 2) {[Environment]::SetEnvironmentVariable($parts[0].Trim(),$parts[1].Trim().Trim('"').Trim("'"),'Process')}
}
function Assert([bool]$condition,[string]$message) {
 if (-not $condition) {throw "FAILED: $message"}
 Write-Host "PASS: $message" -ForegroundColor Green
}
function AcquireToken([string]$client,[string]$secret) {
 if (-not $secret) {throw "Segredo OAuth ausente: $client. Execute setup-keycloak.ps1."}
 $response=Invoke-RestMethod -Method Post -Uri "$($env:OIDC_ISSUER)/protocol/openid-connect/token" -ContentType 'application/x-www-form-urlencoded' -Body @{grant_type='client_credentials';client_id=$client;client_secret=$secret}
 if (-not $response.access_token) {throw "Token nao retornado para $client"}
 return $response.access_token
}
function Send([string]$method,[string]$uri,[string]$token,[object]$body,[string]$key) {
 $headers=@{}
 if ($token) {$headers.Authorization="Bearer $token"}
 if ($key) {$headers['Idempotency-Key']=$key}
 $args=@{Uri=$uri;Method=$method;Headers=$headers;ErrorAction='Stop'}
 if ($null -ne $body) {$args.Body=ConvertTo-Json -InputObject $body -Depth 12 -Compress;$args.ContentType='application/json'}
 try {return @{Code=200;Value=(Invoke-RestMethod @args)}}
 catch {
  $response=$_.Exception.Response
  if ($null -eq $response) {throw}
  return @{Code=[int]$response.StatusCode;Value=$null}
 }
}
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
$base = "http://127.0.0.1:$((Get-Stage8Ports)[0])"
$ready=Invoke-RestMethod "$base/health/ready"
Assert ($ready.status -eq 'ready') 'API ready'
$alpha=AcquireToken 'jungle-provider-alpha' $env:KEYCLOAK_PROVIDER_ALPHA_SECRET
$beta=AcquireToken 'jungle-provider-beta' $env:KEYCLOAK_PROVIDER_BETA_SECRET
$internal=AcquireToken 'jungle-internal' $env:KEYCLOAK_INTERNAL_SECRET
Assert ($alpha -and $beta -and $internal) 'Keycloak: 3 tokens client_credentials obtidos'
$seed=[guid]::NewGuid().ToString('N')
$betID="stage6-bet-$seed"
$key="stage6-idemp-$seed"
$open=@{playerId="stage6-player-$seed";initialBalance=@{amount='100.00';currency='BRL'}}
$opening=Send 'POST' "$base/internal/dev/wallets/open" $internal $open $null
Assert ($opening.Value.playerId -eq $open.playerId) 'Somente cliente interno cria carteira'
$walletID=$opening.Value.id
$denied=Send 'POST' "$base/internal/dev/wallets/open" $alpha $open $null
Assert ($denied.Code -eq 403) 'Provedor nao tem permissao para criar carteiras'
$missing=Send 'POST' "$base/v1/transactions" $null @{} $key
Assert ($missing.Code -eq 401) 'Ausencia de Bearer rejeitada com 401'
$invalid=Send 'GET' "$base/v1/transactions/$betID" 'invalid.jwt.token' $null $null
Assert ($invalid.Code -eq 401) 'Token invalido rejeitado com 401'
$internalWager=Send 'POST' "$base/v1/transactions" $internal @{} $key
Assert ($internalWager.Code -eq 403) 'Cliente interno nao pode apostar como provedor'
$command=@{externalTransactionId=$betID;playerId=$open.playerId;walletId=$walletID;roundId="stage6-round-$seed";gameId='game-a';kind='BET';money=@{amount='10.00';currency='BRL'}}
$wrong=$command.Clone();$wrong.providerId='provider-beta'
$spoof=Send 'POST' "$base/v1/transactions" $alpha $wrong $key
Assert ($spoof.Code -eq 403) 'Tentativa de forjar providerId no JSON rejeitada'
$bet=Send 'POST' "$base/v1/transactions" $alpha $command $key
Assert ($bet.Value.status -eq 'PROCESSED' -and $bet.Value.balance.amount -eq '90.00') 'Aposta do provedor alpha processada: BRL 90.00'
$replay=Send 'POST' "$base/v1/transactions" $alpha $command $key
Assert ($replay.Value.transactionId -eq $bet.Value.transactionId) 'Repeticao idempotente do provedor alpha'
$owned=Send 'GET' "$base/v1/transactions/$betID" $alpha $null $null
Assert ($owned.Value.transactionId -eq $bet.Value.transactionId) 'Alpha consulta apenas sua transacao'
$hidden=Send 'GET' "$base/v1/transactions/$betID" $beta $null $null
Assert ($hidden.Code -eq 404) 'Beta nao consegue consultar transacao de alpha (404)'
$internalRead=Send 'GET' "$base/internal/wallets/$walletID" $internal $null $null
Assert ($internalRead.Value.id -eq $walletID -and $internalRead.Value.balanceMinorUnits -eq 9000) 'Cliente interno consulta saldo BRL 90.00'
$providerRead=Send 'GET' "$base/internal/wallets/$walletID" $beta $null $null
Assert ($providerRead.Code -eq 403) 'Provedor nao consegue consultar carteira interna'
# The same external ID and idempotency key may legitimately belong to another
# provider: each (provider_id, external_id) is independent in PostgreSQL.
$other=@{externalTransactionId=$betID;playerId=$open.playerId;walletId=$walletID;roundId="stage6-round-$seed";gameId='game-b';kind='LOSS';money=@{amount='0.00';currency='BRL'}}
$otherResult=Send 'POST' "$base/v1/transactions" $beta $other $key
Assert ($otherResult.Value.status -eq 'PROCESSED' -and $otherResult.Value.transactionId -ne $bet.Value.transactionId) 'Identificadores iguais nao colidem entre provedores'
$betaOwn=Send 'GET' "$base/v1/transactions/$betID" $beta $null $null
Assert ($betaOwn.Value.transactionId -eq $otherResult.Value.transactionId) 'Beta consulta somente seu proprio resultado'
Write-Host "STAGE 6 PASSED: provider isolation, OIDC, ledger-preserving transaction; wallet=$walletID" -ForegroundColor Green
