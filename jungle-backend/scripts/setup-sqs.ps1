# Idempotent provisioning; never recreates or deletes PostgreSQL or existing queues.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$file=Join-Path $root '.env'
if (-not (Test-Path $file)) {throw 'Copie o .env validado da Etapa 6 para esta pasta primeiro.'}
function LoadEnv {
 Get-Content $file | ForEach-Object {
  $line=$_.Trim();if (-not $line -or $line.StartsWith('#')) {return}
  $p=$line.Split('=',2);if($p.Count -eq 2){[Environment]::SetEnvironmentVariable($p[0].Trim(),$p[1].Trim().Trim('"').Trim("'"),'Process')}
 }
}
function SetEnvValue([string]$name,[string]$value) {
 $lines=@(Get-Content $file)
 if ($lines | Where-Object {$_ -match "^$([regex]::Escape($name))="}) {
  $lines=@($lines | ForEach-Object {if ($_ -match "^$([regex]::Escape($name))=") {"$name=$value"} else {$_}})
  [System.IO.File]::WriteAllLines($file, [string[]]$lines, (New-Object System.Text.UTF8Encoding($false)))
 } else {Add-Content -Path $file -Value "$name=$value" -Encoding UTF8}
 [Environment]::SetEnvironmentVariable($name,$value,'Process')
}
LoadEnv
SetEnvValue 'SQS_ENABLED' 'true'
if (-not $env:SQS_ENDPOINT) {SetEnvValue 'SQS_ENDPOINT' 'http://127.0.0.1:4566'}
if (-not $env:AWS_REGION) {SetEnvValue 'AWS_REGION' 'us-east-1'}
if (-not $env:SQS_INCOMING_QUEUE) {SetEnvValue 'SQS_INCOMING_QUEUE' 'jungle-commands.fifo'}
if (-not $env:SQS_OUTGOING_QUEUE) {SetEnvValue 'SQS_OUTGOING_QUEUE' 'jungle-events.fifo'}
if (-not $env:SQS_DLQ) {SetEnvValue 'SQS_DLQ' 'jungle-commands-dlq.fifo'}
if (-not $env:SQS_COMMAND_KEYS -or $env:SQS_COMMAND_KEYS -eq 'GENERATED_BY_SETUP_SCRIPT' -or $env:SQS_COMMAND_KEYS -like '*SET_YOUR*') {
 function Secret {
  $bytes=New-Object byte[] 32
  $rng=[System.Security.Cryptography.RandomNumberGenerator]::Create()
  try{$rng.GetBytes($bytes)}finally{$rng.Dispose()}
  [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
 }
 $keys="jungle-provider-alpha:$(Secret),jungle-provider-beta:$(Secret)"
 SetEnvValue 'SQS_COMMAND_KEYS' $keys
 Write-Host 'Segredos HMAC gerados no .env local. Nao enviar ao Git.' -ForegroundColor Yellow
}
if ($env:POSTGRES_PORT -ne $env:POSTGRES_COMPOSE_PORT -or $env:OIDC_ISSUER -notin @("http://localhost:$($env:KEYCLOAK_PORT)/realms/jungle", "http://127.0.0.1:$($env:KEYCLOAK_PORT)/realms/jungle")) {
 throw 'Revise .env: portas PostgreSQL e Keycloak devem corresponder ao Docker Compose.'
}
Push-Location $root
try {
 go run ./cmd/sqs-setup
 if ($LASTEXITCODE -ne 0) {throw "Provisionamento SQS falhou com codigo $LASTEXITCODE"}
} finally {Pop-Location}
Write-Host 'SQS Stage 7 pronto. Proximo comando: .\scripts\start-stage7.ps1' -ForegroundColor Green
