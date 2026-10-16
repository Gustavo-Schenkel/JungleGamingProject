# Shared, LOCAL-only Stage 8 helper functions. Dot-source from Stage 8 scripts.
$Stage8Root = Split-Path -Parent $PSScriptRoot
$Stage8RunDir = Join-Path $Stage8Root '.stage8-run'
$Stage8StatePath = Join-Path $Stage8RunDir 'instances.json'
$Stage8BinDir = Join-Path $Stage8Root '.stage8-bin'
$Stage8Exe = Join-Path $Stage8BinDir 'jungle-api-stage8.exe'

function Get-Stage8Ports {
    $configured = $env:API_PORTS
    if (-not $configured) { $configured = '8080,8082,8083' }
    $ports = @($configured.Split(',') | ForEach-Object {
        $number = 0
        if (-not [int]::TryParse($_.Trim(), [ref]$number) -or $number -lt 1024 -or $number -gt 65535) { throw 'API_PORTS deve conter tres portas entre 1024 e 65535.' }
        $number
    })
    if ($ports.Count -ne 3 -or @($ports | Select-Object -Unique).Count -ne 3) { throw 'API_PORTS exige tres portas distintas.' }
    return $ports
}

function Import-Stage8Env {
    $path = Join-Path $Stage8Root '.env'
    if (-not (Test-Path $path)) { throw 'Copie o .env validado da Etapa 7 para esta pasta.' }
    foreach ($line in Get-Content $path) {
        $s = $line.Trim()
        if (-not $s -or $s.StartsWith('#')) { continue }
        $p = $s.Split('=', 2)
        if ($p.Count -ne 2 -or $p[0].Trim() -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { throw 'Linha invalida no arquivo .env.' }
        [Environment]::SetEnvironmentVariable($p[0].Trim(), $p[1].Trim().Trim('"').Trim("'"), 'Process')
    }
    if ($env:POSTGRES_HOST -notin @('localhost','127.0.0.1') -or $env:POSTGRES_PORT -ne $env:POSTGRES_COMPOSE_PORT) { throw 'POSTGRES_PORT deve corresponder a POSTGRES_COMPOSE_PORT local.' }
    if ($env:APP_ENV -ne 'development' -or $env:OIDC_ISSUER -notin @("http://localhost:$($env:KEYCLOAK_PORT)/realms/jungle", "http://127.0.0.1:$($env:KEYCLOAK_PORT)/realms/jungle")) { throw 'Confira APP_ENV e OIDC_ISSUER com a porta local do Keycloak.' }
    if ($env:SQS_ENABLED -ne 'true' -or $env:SQS_ENDPOINT -ne "http://127.0.0.1:$($env:LOCALSTACK_PORT)" -or $env:SQS_COMMAND_KEYS -eq 'GENERATED_BY_SETUP_SCRIPT' -or -not $env:SQS_COMMAND_KEYS) { throw 'Execute scripts/setup-sqs.ps1 primeiro.' }
    if (-not $env:POSTGRES_PASSWORD -or $env:POSTGRES_PASSWORD -eq 'SET_YOUR_LOCAL_PASSWORD') { throw 'Credenciais do PostgreSQL nao configuradas no .env.' }
}

function Get-Stage8Instances {
    if (-not (Test-Path $Stage8StatePath)) { return @() }
    $data = Get-Content $Stage8StatePath -Raw | ConvertFrom-Json
    return @($data.instances)
}

function Save-Stage8Instances([array]$entries) {
    New-Item -ItemType Directory -Force -Path $Stage8RunDir | Out-Null
    @{ schemaVersion = 1; instances = @($entries) } | ConvertTo-Json -Depth 7 | Set-Content -Path $Stage8StatePath -Encoding UTF8
}

function Test-Stage8OwnedProcess($entry) {
    $proc = Get-Process -Id ([int]$entry.pid) -ErrorAction SilentlyContinue
    if (-not $proc) { return $false }
    try {
        if (-not $proc.Path -or -not [string]::Equals($proc.Path, $Stage8Exe, [StringComparison]::OrdinalIgnoreCase)) { return $false }
        if ($entry.startedAtTicks) { return $proc.StartTime.ToUniversalTime().Ticks -eq [long]::Parse([string]$entry.startedAtTicks, [Globalization.CultureInfo]::InvariantCulture) }
        if ($entry.startedAtUtc -is [datetime]) {
    $recorded = $entry.startedAtUtc.ToUniversalTime()
} else {
    $recorded = [DateTimeOffset]::Parse(
        [string]$entry.startedAtUtc,
        [System.Globalization.CultureInfo]::InvariantCulture
    ).UtcDateTime
}
        $actual = $proc.StartTime.ToUniversalTime()
        return ([Math]::Abs(($actual-$recorded).TotalMilliseconds) -lt 10)
    } catch { return $false }
}

function Assert-Stage8PortFree([int]$port) {
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue)
    if ($listeners.Count -gt 0) { throw "Porta $port ja ocupada. Encerre a API anterior com Ctrl+C; nao encerraremos processos desconhecidos." }
}

function Wait-Stage8Ready([int]$port, [int]$seconds = 90) {
    $until = [DateTime]::UtcNow.AddSeconds($seconds)
    while ([DateTime]::UtcNow -lt $until) {
        try {
            $result = Invoke-RestMethod -Method Get -Uri "http://127.0.0.1:$port/health/ready" -TimeoutSec 2
            if ($result.status -eq 'ready') { return }
        } catch { }
        Start-Sleep -Milliseconds 550
    }
    throw "API $port nao ficou ready em $seconds segundos. Consulte .stage8-run\api-$port.stderr.log."
}
