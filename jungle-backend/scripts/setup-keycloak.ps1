# Stage 6 - provision the jungle realm and three OAuth2 service accounts.
# Requires Keycloak 26 at localhost:8081 and KEYCLOAK_ADMIN(_PASSWORD) in .env.
# Does NOT recreate Docker containers or modify the PostgreSQL database.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$envFile = Join-Path $root '.env'
if (-not (Test-Path $envFile)) { throw 'Copie .env.example para .env antes de executar.' }
Get-Content $envFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -eq '' -or $line.StartsWith('#')) { return }
    $parts = $line.Split('=',2)
    if ($parts.Count -ne 2) { return }
    $name = $parts[0].Trim()
    $value = $parts[1].Trim().Trim('"').Trim("'")
    [Environment]::SetEnvironmentVariable($name,$value,'Process')
}
if (-not $env:KEYCLOAK_ADMIN_PASSWORD -or $env:KEYCLOAK_ADMIN_PASSWORD -eq 'SET_YOUR_KEYCLOAK_ADMIN_PASSWORD' -or $env:KEYCLOAK_ADMIN_PASSWORD -eq 'CHANGE_ME') {
    throw 'Configure a senha REAL de administrador do Keycloak em KEYCLOAK_ADMIN_PASSWORD no .env.'
}
if (-not $env:KEYCLOAK_ADMIN) { $env:KEYCLOAK_ADMIN = 'admin' }
$base = ([Uri]$env:OIDC_ISSUER).GetLeftPart([UriPartial]::Authority)
try {
    $adminToken = (Invoke-RestMethod -Uri "$base/realms/master/protocol/openid-connect/token" -Method Post -Body @{
       grant_type='password';client_id='admin-cli';username=$env:KEYCLOAK_ADMIN;password=$env:KEYCLOAK_ADMIN_PASSWORD
    } -ContentType 'application/x-www-form-urlencoded').access_token
} catch {
    throw 'Falha ao obter token de administrador: confirme se jungle-keycloak esta ativo e KEYCLOAK_ADMIN_PASSWORD corresponde ao container.'
}
$headers = @{ Authorization = "Bearer $adminToken" }
function AdminGet([string]$path) {
    Invoke-RestMethod -Uri "$base$path" -Method Get -Headers $headers
}
function AdminWrite([string]$method, [string]$path, [object]$body) {
    $json = ConvertTo-Json -InputObject $body -Depth 15 -Compress
    Invoke-RestMethod -Uri "$base$path" -Method $method -Headers $headers -ContentType 'application/json' -Body $json | Out-Null
}
# Idempotent creation; never delete existing realm users or credentials.
$realmFound = $true
try { $null = AdminGet '/admin/realms/jungle' } catch {
    if ([int]$_.Exception.Response.StatusCode -eq 404) { $realmFound = $false } else { throw }
}
if (-not $realmFound) {
    AdminWrite 'Post' '/admin/realms' @{realm='jungle';enabled=$true;accessTokenLifespan=300}
    Write-Host 'Realm jungle criado.' -ForegroundColor Green
} else { Write-Host 'Realm jungle ja existe; sera preservado.' -ForegroundColor Cyan }
function GenerateSecret {
    $bytes = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
}
$clients = @(
    @{ Id='jungle-provider-alpha'; Var='KEYCLOAK_PROVIDER_ALPHA_SECRET' },
    @{ Id='jungle-provider-beta'; Var='KEYCLOAK_PROVIDER_BETA_SECRET' },
    @{ Id='jungle-internal'; Var='KEYCLOAK_INTERNAL_SECRET' }
)
foreach ($client in $clients) {
    $secret = [Environment]::GetEnvironmentVariable($client.Var,'Process')
    if (-not $secret -or $secret -eq 'GENERATED_BY_SETUP_SCRIPT') {
       $secret = GenerateSecret
       Add-Content -Path $envFile -Value "$($client.Var)=$secret"
       [Environment]::SetEnvironmentVariable($client.Var,$secret,'Process')
    }
    $existing = @(AdminGet "/admin/realms/jungle/clients?clientId=$($client.Id)" | Where-Object { $_.clientId -eq $client.Id })
    $body = @{
        clientId=$client.Id;enabled=$true;protocol='openid-connect';publicClient=$false;
        clientAuthenticatorType='client-secret';secret=$secret;serviceAccountsEnabled=$true;
        standardFlowEnabled=$false;directAccessGrantsEnabled=$false;implicitFlowEnabled=$false;
        fullScopeAllowed=$false
    }
    if ($existing.Count -eq 0) {
        AdminWrite 'Post' '/admin/realms/jungle/clients' $body
    } else {
        AdminWrite 'Put' "/admin/realms/jungle/clients/$($existing[0].id)" $body
    }
    $found = @(AdminGet "/admin/realms/jungle/clients?clientId=$($client.Id)" | Where-Object { $_.clientId -eq $client.Id })
    if ($found.Count -ne 1) { throw "Nao foi possivel localizar o cliente $($client.Id)" }
    $uuid = $found[0].id
    # Audience mapper ensures tokens are specifically intended for THIS API.
    $mappers = @(AdminGet "/admin/realms/jungle/clients/$uuid/protocol-mappers/models")
    if (-not ($mappers | Where-Object { $_.name -eq 'jungle-api-audience' })) {
        AdminWrite 'Post' "/admin/realms/jungle/clients/$uuid/protocol-mappers/models" @{
            name='jungle-api-audience';protocol='openid-connect';protocolMapper='oidc-audience-mapper';
            config=@{'included.custom.audience'='jungle-api';'access.token.claim'='true';'id.token.claim'='false'}
        }
    }
    Write-Host "Cliente configurado: $($client.Id) (segredo omitido)" -ForegroundColor Green
}
Write-Host 'Realm e tres clients preparados. Segredos salvos no .env local (NAO enviar ao Git).' -ForegroundColor Green
