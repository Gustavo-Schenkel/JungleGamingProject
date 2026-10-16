$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Get-Content (Join-Path $root '.env') | ForEach-Object {
 $s=$_.Trim();if(-not $s -or $s.StartsWith('#')){return}
 $p=$s.Split('=',2);if($p.Count -eq 2){[Environment]::SetEnvironmentVariable($p[0].Trim(),$p[1].Trim().Trim('"').Trim("'"),'Process')}
}
$env:Path="C:\msys64\ucrt64\bin;$env:Path"
$env:CGO_ENABLED='1';$env:CC='gcc';$env:RUN_DB_INTEGRATION='1'
if($env:POSTGRES_PORT -ne '5433'){throw 'Use PostgreSQL Docker na porta 5433.'}
Push-Location $root
try {
 Write-Host 'PostgreSQL inbox/ledger/outbox atomicity + race detector' -ForegroundColor Cyan
 go test -race -count=1 ./internal/finance ./internal/messaging
 if($LASTEXITCODE -ne 0){throw 'Stage 7 DB/race integration FAILED'}
 Write-Host 'STAGE 7 DB INTEGRATION PASSED' -ForegroundColor Green
} finally {Pop-Location}
