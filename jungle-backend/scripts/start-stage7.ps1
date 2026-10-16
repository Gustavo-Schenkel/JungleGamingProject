$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$envFile=Join-Path $root '.env'
if (-not (Test-Path $envFile)) {throw 'Falta .env: copie .env.example e configure suas credenciais locais.'}
Get-Content $envFile | ForEach-Object {
 $line=$_.Trim()
 if ($line -eq '' -or $line.StartsWith('#')) {return}
 $parts=$line.Split('=',2)
 if ($parts.Count -ne 2 -or $parts[0].Trim() -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {throw "Linha invalida no .env: $line"}
 $name=$parts[0].Trim();$value=$parts[1].Trim().Trim('"').Trim("'")
 [Environment]::SetEnvironmentVariable($name,$value,'Process')
}
if ($env:POSTGRES_HOST -notin @('localhost','127.0.0.1') -or $env:POSTGRES_PORT -ne '5433') {throw 'Conecte ao PostgreSQL Docker localhost:5433, nao ao Windows:5432.'}
if ($env:APP_ENV -ne 'development' -or $env:APP_ADDR -ne '127.0.0.1:8080') {throw 'Etapa 7 preparada para development e localhost:8080.'}
if (-not $env:POSTGRES_PASSWORD -or $env:POSTGRES_PASSWORD -eq 'SET_YOUR_LOCAL_PASSWORD') {throw 'Configure a senha do PostgreSQL Docker no .env.'}
if ($env:OIDC_ISSUER -ne 'http://localhost:8081/realms/jungle') {throw 'OIDC_ISSUER deve ser http://localhost:8081/realms/jungle nesta etapa.'}
if ($env:OIDC_AUDIENCE -ne 'jungle-api' -or -not $env:OIDC_PROVIDER_CLIENTS -or -not $env:OIDC_INTERNAL_CLIENT) {throw 'Configure OIDC_AUDIENCE, OIDC_PROVIDER_CLIENTS e OIDC_INTERNAL_CLIENT.'}
if ($env:SQS_ENABLED -ne 'true' -or -not $env:SQS_COMMAND_KEYS -or $env:SQS_ENDPOINT -ne 'http://127.0.0.1:4566') {throw 'Execute .\scripts\setup-sqs.ps1 e mantenha SQS_ENDPOINT no LocalStack local.'}
Write-Host "Banco: $($env:POSTGRES_HOST):$($env:POSTGRES_PORT)/$($env:POSTGRES_DB); OIDC: $($env:OIDC_ISSUER)" -ForegroundColor Cyan
Push-Location $root
try { go run ./cmd/api; if ($LASTEXITCODE -ne 0) {throw "Go retornou codigo $LASTEXITCODE"} } finally {Pop-Location}
