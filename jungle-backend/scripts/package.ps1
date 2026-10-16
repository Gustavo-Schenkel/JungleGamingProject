# Build a source-only archive from an explicit allowlist, including dotfiles.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$output = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $output | Out-Null
$files = @()
foreach ($directory in @('cmd','internal','scripts','docs')) {
    $files += Get-ChildItem -LiteralPath (Join-Path $root $directory) -Recurse -File
}
foreach ($name in @('go.mod','go.sum','Dockerfile','docker-compose.yml','.env.example','.gitignore','.dockerignore','README.md','ARCHITECTURE.md')) {
    $files += Get-Item -LiteralPath (Join-Path $root $name)
}
foreach ($file in $files) {
    if ($file.Name -eq '.env' -or $file.Extension -in @('.exe','.backup','.log','.pem','.key') -or $file.FullName -match '[\\/]\.stage8-') {
        throw "Forbidden release artifact: $($file.Name)"
    }
}
$manifest = @($files | Sort-Object FullName | ForEach-Object {
    $relative = $_.FullName.Substring($root.Length + 1).Replace('\','/')
    (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant() + '  ' + $relative
})
$manifestPath = Join-Path $root 'MANIFEST.sha256'
[IO.File]::WriteAllLines($manifestPath,[string[]]$manifest,(New-Object Text.UTF8Encoding($false)))
$files += Get-Item $manifestPath
$zipPath = Join-Path $output 'jungle-backend-final.zip'
if (Test-Path $zipPath) { throw 'Archive already exists. Rename it before creating a new release.' }
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::Open($zipPath,[IO.Compression.ZipArchiveMode]::Create)
try {
    foreach ($file in $files) {
        $relative = $file.FullName.Substring($root.Length + 1).Replace('\','/')
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,$file.FullName,"jungle-backend/$relative",[IO.Compression.CompressionLevel]::Optimal) | Out-Null
    }
} finally { $zip.Dispose() }
$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText(($zipPath + '.sha256'),"$hash  jungle-backend-final.zip`n",(New-Object Text.UTF8Encoding($false)))
Write-Host "Created $zipPath (SHA-256 $hash)"
