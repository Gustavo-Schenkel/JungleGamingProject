$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'stage8-common.ps1')
Import-Stage8Env
$instances = @(Get-Stage8Instances)
if ($instances.Count -ne 3) { throw 'Inicie as 3 instancias com .\scripts\start-stage8.ps1 antes deste teste.' }
$uniquePids = @($instances | ForEach-Object { $_.pid } | Select-Object -Unique)
if ($uniquePids.Count -ne 3) { throw 'As tres instancias devem possuir PIDs distintos.' }
$ports = @(Get-Stage8Ports)
foreach ($port in $ports) {
    $item = @($instances | Where-Object { $_.port -eq $port })
    if ($item.Count -ne 1 -or -not (Test-Stage8OwnedProcess $item[0])) { throw "Processo gerenciado na porta $port nao encontrado. Confira .stage8-run/." }
}
Write-Host 'PASS: tres processos Go independentes com PIDs distintos.' -ForegroundColor Green
Push-Location $Stage8Root
try {
    $urls = ($ports | ForEach-Object { "http://127.0.0.1:$_" }) -join ','
    go run ./cmd/stage8-verify -mode full -urls $urls -out '.stage8-evidence/stage8-full.json'
    if ($LASTEXITCODE -ne 0) { throw 'STAGE 8 distributed validation FAILED. Consulte o relatorio .stage8-evidence/stage8-full.json.' }
} finally { Pop-Location }
