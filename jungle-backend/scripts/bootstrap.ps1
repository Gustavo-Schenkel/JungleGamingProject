# Clean-machine local setup. Existing .env values and Docker volumes are preserved.
param(
    [switch]$Start,
    [ValidateRange(30,3600)][int]$ServiceTimeoutSeconds = 900,
    [ValidatePattern('^[a-z][a-z0-9-]{2,40}$')][string]$InstanceName,
    [ValidateRange(0,50000)][int]$PortOffset = 0,
    [switch]$CheckOnly
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$file = Join-Path $root '.env'
foreach ($tool in @('go','docker')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Install $tool and reopen PowerShell." }
}
if (-not (Test-Path $file)) { Copy-Item (Join-Path $root '.env.example') $file }
# Older stage configurations contain credentials but may lack release settings.
# Add missing defaults without replacing any existing credential or value.
$existingLines = @(Get-Content -LiteralPath $file)
$keys = @{}
foreach ($line in $existingLines) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=') { $keys[$Matches[1]] = $true }
}
foreach ($line in Get-Content (Join-Path $root '.env.example')) {
    if ($line -match '^([A-Za-z_][A-Za-z0-9_]*)=' -and -not $keys.ContainsKey($Matches[1])) {
        $existingLines += $line
    }
}
if ($InstanceName -or $PortOffset -gt 0) {
    $statePath = Join-Path $root '.stage8-run/instances.json'
    if ((Test-Path $statePath) -and @((Get-Content $statePath -Raw | ConvertFrom-Json).instances).Count -gt 0) {
        throw 'Stop this installation with scripts/stop-stage8.ps1 before changing its profile.'
    }
    $settings = @{}
    if ($InstanceName) {
        $settings.COMPOSE_PROJECT_NAME = $InstanceName
        $settings.POSTGRES_CONTAINER = "$InstanceName-postgres"
        $settings.KEYCLOAK_CONTAINER = "$InstanceName-keycloak"
        $settings.LOCALSTACK_CONTAINER = "$InstanceName-localstack"
    }
    if ($PortOffset -gt 0) {
        $settings.POSTGRES_PORT = 5433 + $PortOffset
        $settings.POSTGRES_COMPOSE_PORT = 5433 + $PortOffset
        $settings.KEYCLOAK_PORT = 8081 + $PortOffset
        $settings.LOCALSTACK_PORT = 4566 + $PortOffset
        $settings.OIDC_ISSUER = "http://127.0.0.1:$(8081 + $PortOffset)/realms/jungle"
        $settings.SQS_ENDPOINT = "http://127.0.0.1:$(4566 + $PortOffset)"
        $settings.APP_ADDR = "127.0.0.1:$(8080 + $PortOffset)"
        $settings.API_PORTS = (@(8080,8082,8083) | ForEach-Object { $_ + $PortOffset }) -join ','
    }
    $existingLines = @($existingLines | ForEach-Object {
        $key = ($_ -split '=',2)[0].Trim()
        if ($settings.ContainsKey($key)) { "$key=$($settings[$key])" } else { $_ }
    })
}
[IO.File]::WriteAllLines($file,[string[]]$existingLines,[Text.UTF8Encoding]::new($false))
function NewLocalSecret {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return ([BitConverter]::ToString($bytes)).Replace('-','').ToLowerInvariant()
}
$lines = @(Get-Content $file)
$lines = @($lines | ForEach-Object {
    if ($_ -match '^POSTGRES_PASSWORD=(SET_YOUR_LOCAL_PASSWORD|CHANGE_ME)?$') { 'POSTGRES_PASSWORD=' + (NewLocalSecret) }
    elseif ($_ -match '^KEYCLOAK_ADMIN_PASSWORD=(CHANGE_ME|SET_YOUR_KEYCLOAK_ADMIN_PASSWORD)?$') { 'KEYCLOAK_ADMIN_PASSWORD=' + (NewLocalSecret) }
    else { $_ }
})
[IO.File]::WriteAllLines($file, [string[]]$lines, (New-Object Text.UTF8Encoding($false)))
Push-Location $root
try {
    # Load only configuration; full Stage 8 assertions require generated HMAC keys.
    foreach ($line in Get-Content $file) {
        if ($line.Trim() -and -not $line.Trim().StartsWith('#')) {
            $pair = $line.Split('=',2)
            if ($pair.Count -eq 2) { [Environment]::SetEnvironmentVariable($pair[0].Trim(),$pair[1].Trim().Trim('"').Trim("'"),'Process') }
        }
    }
    docker compose config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Invalid Compose configuration.' }
    $allJson = docker ps -a --format json
    if ($LASTEXITCODE -ne 0) { throw 'Docker Desktop is unavailable. Start it with Linux containers.' }
    $containers = @($allJson | ForEach-Object { $_ | ConvertFrom-Json })
    $bindings = @(
        @{ Port = [int]$env:POSTGRES_COMPOSE_PORT; Name = $env:POSTGRES_CONTAINER; Service = 'postgres' },
        @{ Port = [int]$env:KEYCLOAK_PORT; Name = $env:KEYCLOAK_CONTAINER; Service = 'keycloak' },
        @{ Port = [int]$env:LOCALSTACK_PORT; Name = $env:LOCALSTACK_CONTAINER; Service = 'localstack' }
    )
    foreach ($binding in $bindings) {
        $existing = @($containers | Where-Object { $_.Names -eq $binding.Name })
        if ($existing.Count) {
            $identityJson = docker inspect --format '{{json .Config.Labels}}' $binding.Name
            if ($LASTEXITCODE -ne 0) { throw "Cannot inspect $($binding.Name)." }
            $identity = $identityJson | ConvertFrom-Json
            if ($identity.'com.docker.compose.project' -ne $env:COMPOSE_PROJECT_NAME -or
                $identity.'com.docker.compose.service' -ne $binding.Service) {
                throw "Container $($binding.Name) belongs to another installation. Choose an unused -InstanceName and free -PortOffset; existing services were preserved."
            }
            if ($existing[0].State -eq 'running' -and $existing[0].Ports -match (':' + $binding.Port + '->')) { continue }
        }
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,$binding.Port)
        try { $listener.Start() }
        catch { throw "Port $($binding.Port) is unavailable. Choose a free -PortOffset or edit matching ports in .env. No containers have been started." }
        finally { $listener.Stop() }
    }
    if ($Start) {
        . (Join-Path $PSScriptRoot 'stage8-common.ps1')
        $managed = @(Get-Stage8Instances)
        foreach ($port in @(Get-Stage8Ports)) {
            $entry = @($managed | Where-Object { [int]$_.port -eq $port })
            if ($entry.Count) {
                if ($entry.Count -ne 1 -or -not (Test-Stage8OwnedProcess $entry[0])) {
                    throw "Stale process record for port $port; inspect it before restarting."
                }
            } else { Assert-Stage8PortFree $port }
        }
    }
    if ($CheckOnly) { Write-Host 'PASS: configuration, container ownership and requested ports. No services started.'; return }
    # Readiness below uses the configured deadline, not an image's short grace period.
    docker compose up -d
    if ($LASTEXITCODE -ne 0) { throw 'Docker startup failed. Check Docker Desktop, ports and compose logs.' }
    $deadline = [DateTime]::UtcNow.AddSeconds($ServiceTimeoutSeconds)
    Write-Host 'Waiting for PostgreSQL readiness...'
    do {
        docker exec $env:POSTGRES_CONTAINER pg_isready -U $env:POSTGRES_USER -d $env:POSTGRES_DB | Out-Null
        if ($LASTEXITCODE -eq 0) { break }
        if ([DateTime]::UtcNow -ge $deadline) { throw 'PostgreSQL readiness deadline exceeded. Inspect docker compose logs postgres.' }
        Start-Sleep -Seconds 3
    } while ($true)
    foreach ($address in @($env:OIDC_ISSUER.Replace('/realms/jungle','/realms/master'), ($env:SQS_ENDPOINT + '/_localstack/health'))) {
        $ready = $false
        $deadline = [DateTime]::UtcNow.AddSeconds($ServiceTimeoutSeconds)
        Write-Host "Waiting for $address (up to $ServiceTimeoutSeconds seconds)..."
        $nextProgress = [DateTime]::UtcNow.AddSeconds(30)
        while ([DateTime]::UtcNow -lt $deadline) {
            try {
                $response = Invoke-RestMethod -Uri $address -TimeoutSec 15
                if ($address.EndsWith('/_localstack/health') -and $response.services.sqs -notin @('available','running')) {
                    throw 'SQS is not ready yet.'
                }
                $ready = $true; break
            } catch { Start-Sleep -Seconds 3 }
            if ([DateTime]::UtcNow -ge $nextProgress) {
                Write-Host "Still waiting for $address..."
                $nextProgress = [DateTime]::UtcNow.AddSeconds(30)
            }
        }
        if (-not $ready) { throw "Service did not become ready: $address" }
    }
    & (Join-Path $PSScriptRoot 'setup-keycloak.ps1')
    # Realm discovery/JWKS can be cold even after the admin API is responsive.
    $discovery = Invoke-RestMethod -Uri ($env:OIDC_ISSUER + '/.well-known/openid-configuration') -TimeoutSec 60
    if ($discovery.issuer -ne $env:OIDC_ISSUER) { throw 'OIDC discovery issuer differs from OIDC_ISSUER.' }
    $null = Invoke-RestMethod -Uri $discovery.jwks_uri -TimeoutSec 60
    & (Join-Path $PSScriptRoot 'setup-sqs.ps1')
    if ($Start) {
        . (Join-Path $PSScriptRoot 'stage8-common.ps1')
        Import-Stage8Env
        $configuredPorts = @(Get-Stage8Ports)
        $running = @(Get-Stage8Instances)
        foreach ($entry in $running) {
            if (-not (Test-Stage8OwnedProcess $entry)) { throw 'Recorded process is not running or its identity changed; inspect it before restarting.' }
            Wait-Stage8Ready ([int]$entry.port)
        }
        $missing = @($configuredPorts | Where-Object { $_ -notin @($running | ForEach-Object { [int]$_.port }) })
        if ($missing.Count -gt 0) { & (Join-Path $PSScriptRoot 'start-stage8.ps1') -Ports $missing }
    }
} finally { Pop-Location }
Write-Host 'Bootstrap complete. Local credentials are in .env; keep this file private.' -ForegroundColor Green
