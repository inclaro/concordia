<#
.SYNOPSIS
Installs Concordia from a public GitHub release of inclaro/concordia, then installs the pinned codebase-memory-mcp (CBM).

.DESCRIPTION
Downloads the release over HTTPS. No GitHub login is needed.
When cosign is on the PATH, the script verifies the signature of the checksum file. Without cosign it checks the
checksum only and prints a warning.
After Concordia is installed, the script runs `concordia cbm install --dir <dir>`. Concordia never replaces a
different codebase-memory-mcp build. When another build is found, the script reports it and exits with a non-zero
code after Concordia is installed.

.PARAMETER Version
Release tag, for example v1.2.3. Default: the latest release.

.PARAMETER Dir
Install directory for concordia.exe and codebase-memory-mcp.exe.
Default: $env:LOCALAPPDATA\Programs\codebase-memory-mcp.
#>
[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$Dir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3.0

$repository = 'inclaro/concordia'
$stage = $null

function Stop-Install {
    param([Parameter(Mandatory = $true)][string]$Message)
    throw "install.ps1: $Message"
}

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$Command,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        Stop-Install "command failed with exit code ${LASTEXITCODE}: $Command $($Arguments -join ' ')"
    }
}

if ([string]::IsNullOrEmpty($Dir)) {
    if ([string]::IsNullOrEmpty($env:LOCALAPPDATA)) {
        Stop-Install 'LOCALAPPDATA is not set; use -Dir'
    }
    $Dir = Join-Path $env:LOCALAPPDATA 'Programs\codebase-memory-mcp'
}

$goarch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    default { Stop-Install "unsupported architecture: $env:PROCESSOR_ARCHITECTURE (Concordia is built for windows/amd64)" }
}

if ([string]::IsNullOrEmpty($Version)) {
    try {
        $Version = [string](Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repository/releases/latest").tag_name
    } catch {
        $Version = ''
    }
    if ([string]::IsNullOrEmpty($Version)) {
        Stop-Install "cannot find the latest release of $repository"
    }
}
$tag = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
$archiveName = "concordia_$($tag.Substring(1))_windows_$goarch.zip"
$checksumsName = 'concordia_checksums.txt'
$signatureName = "$checksumsName.sigstore.json"

$stage = Join-Path ([System.IO.Path]::GetTempPath()) ('concordia-install.' + [System.Guid]::NewGuid().ToString('N'))
$extract = Join-Path $stage 'extract'
New-Item -ItemType Directory -Path $extract -Force | Out-Null
try {
    Write-Output "Downloading Concordia $tag (windows/$goarch)"
    foreach ($name in @($archiveName, $checksumsName, $signatureName)) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/$repository/releases/download/$tag/$name" -OutFile (Join-Path $stage $name)
        } catch {
            Stop-Install "cannot download release $tag of $repository"
        }
    }

    $checksums = Join-Path $stage $checksumsName
    $cosign = Get-Command cosign -ErrorAction SilentlyContinue
    if ($null -ne $cosign) {
        Invoke-Native -Command $cosign.Source -Arguments @(
            'verify-blob', '--bundle', (Join-Path $stage $signatureName),
            '--certificate-identity', "https://github.com/$repository/.github/workflows/release.yml@refs/tags/$tag",
            '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com', $checksums)
    } else {
        [Console]::Error.WriteLine('install.ps1: warning: cosign is not on the PATH; the signature is NOT verified, only the checksum.')
        [Console]::Error.WriteLine('install.ps1: warning: install cosign (https://docs.sigstore.dev/) for a verified install.')
    }

    $expected = $null
    foreach ($line in [System.IO.File]::ReadAllLines($checksums)) {
        if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+)$' -and $Matches[2] -eq $archiveName) {
            $expected = $Matches[1].ToLowerInvariant()
            break
        }
    }
    if ([string]::IsNullOrEmpty($expected)) {
        Stop-Install "$checksumsName has no entry for $archiveName"
    }
    $archive = Join-Path $stage $archiveName
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant() -ne $expected) {
        Stop-Install "checksum verification failed for $archiveName"
    }

    Expand-Archive -LiteralPath $archive -DestinationPath $extract -Force
    $source = Join-Path $extract 'concordia.exe'
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        Stop-Install "$archiveName does not contain concordia.exe"
    }
    New-Item -ItemType Directory -Path $Dir -Force | Out-Null
    $partial = Join-Path $Dir '.concordia.exe.new'
    $destination = Join-Path $Dir 'concordia.exe'
    Copy-Item -LiteralPath $source -Destination $partial -Force
    Move-Item -LiteralPath $partial -Destination $destination -Force
    Write-Output "Installed concordia $tag to $destination"
} finally {
    if ($null -ne $stage -and (Test-Path -LiteralPath $stage)) {
        Remove-Item -LiteralPath $stage -Recurse -Force
    }
}

# The pinned CBM build and its checks live in the Go command only.
& $destination cbm install --dir $Dir
if ($LASTEXITCODE -ne 0) {
    [Console]::Error.WriteLine('install.ps1: concordia is installed, but concordia cbm install failed (see above).')
    exit 1
}
$onPath = ($env:PATH -split ';') | Where-Object { $_.TrimEnd('\') -eq $Dir.TrimEnd('\') }
if (-not $onPath) {
    [Console]::Error.WriteLine("install.ps1: warning: $Dir is not on your PATH.")
}
