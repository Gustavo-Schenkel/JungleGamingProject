# Inicie no PowerShell 5.1+ dentro da pasta stage5-revisada.
# Le as variaveis locais (sem depender de set-ExecutionPolicy global).
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$envFile = Join-Path $root '.env'
if (-not (Test-Path $envFile)) {
    throw 'Arquivo .env ausente. Execute: Copy-Item .env.example .env ; depois edite a senha no .env.'
}
Get-Content $envFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -eq '' -or $line.StartsWith('#')) { return }
    $parts = $line.Split('=', 2)
    if ($parts.Count -ne 2 -or $parts[0].Trim() -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
        throw "Linha invalida no .env: $line"
    }
    $name = $parts[0].Trim()
    $value = $parts[1].Trim()
    if ($value.Length -ge 2 -and (($value[0] -eq '"' -and $value[-1] -eq '"') -or ($value[0] -eq "'" -and $value[-1] -eq "'"))) {
        $value = $value.Substring(1, $value.Length - 2)
    }
    [Environment]::SetEnvironmentVariable($name, $value, 'Process')
}
if ($env:POSTGRES_PORT -ne '5433' -or $env:POSTGRES_HOST -notin @('localhost','127.0.0.1')) {
    throw 'Configuracao insegura: esta etapa deve usar PostgreSQL Docker em 127.0.0.1:5433. Verifique o .env.'
}
if ($env:APP_ENV -ne 'development') { throw 'Etapa 5 somente em APP_ENV=development.' }
if ($env:APP_ADDR -ne '127.0.0.1:8080') { throw 'Etapa 5 deve escutar exclusivamente em 127.0.0.1:8080.' }
if ($env:POSTGRES_PASSWORD -eq '' -or $env:POSTGRES_PASSWORD -eq 'SET_YOUR_LOCAL_PASSWORD') {
    throw 'Edite POSTGRES_PASSWORD no .env para a senha do seu PostgreSQL Docker.'
}
Write-Host "GO -> PostgreSQL $($env:POSTGRES_HOST):$($env:POSTGRES_PORT)/$($env:POSTGRES_DB) | usuario=$($env:POSTGRES_USER)" -ForegroundColor Cyan
Write-Host 'Senha omitida por seguranca. Iniciando Go...' -ForegroundColor Cyan
Push-Location $root
try {
    go run ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw "go run retornou codigo $LASTEXITCODE" }
} finally {
    Pop-Location
}
