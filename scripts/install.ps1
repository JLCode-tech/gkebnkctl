<#
.SYNOPSIS
  gkebnkctl installer for Windows.

.DESCRIPTION
  Downloads a release .zip for this architecture, verifies its checksum, extracts
  gkebnkctl.exe, and hands off to the binary's own `gkebnkctl install` (which
  copies it onto PATH, replacing any existing copy). The archive + extracted
  binary are then removed, leaving only the installed binary.

  Run it directly:
    irm https://raw.githubusercontent.com/JLCode-tech/gkebnkctl/main/scripts/install.ps1 | iex

  Or with a specific version / install args:
    $env:VERSION='v1.0.0'; irm https://raw.githubusercontent.com/JLCode-tech/gkebnkctl/main/scripts/install.ps1 | iex
#>
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repo = 'JLCode-tech/gkebnkctl'
$Version = if ($env:VERSION) { $env:VERSION } else { '' }
$InstallArgs = if ($env:GKEBNKCTL_INSTALL_ARGS) { $env:GKEBNKCTL_INSTALL_ARGS } else { '' }

# ---- architecture (goreleaser naming) --------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'ARM64' { 'arm64' }
  default { 'amd64' }
}

# ---- resolve auth token if available ---------------------------------------
$headers = @{ 'User-Agent' = 'gkebnkctl' }
$token = if ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN } elseif ($env:GH_TOKEN) { $env:GH_TOKEN } else { '' }
if (-not $token -and (Get-Command gh -ErrorAction SilentlyContinue)) {
  try { $token = (& gh auth token 2>$null).Trim() } catch {}
}
if ($token) {
  $headers['Authorization'] = "Bearer $token"
}

# ---- resolve the release version -------------------------------------------
if (-not $Version) {
  $latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
  $Version = $latest.tag_name
}
if (-not $Version) { throw 'gkebnkctl: could not resolve a release version' }
$verNoV = $Version.TrimStart('v')

$asset = "gkebnkctl_${verNoV}_windows_${arch}.zip"
$base = "https://github.com/$Repo/releases/download/$Version"

# ---- download into a temp dir that is cleaned up in finally -----------------
$tmp = Join-Path $env:TEMP ("gkebnkctl-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  Write-Host "Downloading gkebnkctl $Version (windows/$arch)..."
  $zip = Join-Path $tmp $asset
  Invoke-WebRequest -Uri "$base/$asset" -OutFile $zip -Headers $headers -UseBasicParsing

  # checksum (best-effort; skipped if checksums.txt is absent)
  try {
    $sumsPath = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -Headers $headers -UseBasicParsing
    $line = Select-String -Path $sumsPath -Pattern ([regex]::Escape($asset)) | Select-Object -First 1
    if ($line) {
      $want = ($line.Line -split '\s+')[0]
      $got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLower()
      if ($want.ToLower() -ne $got) { throw "gkebnkctl: checksum mismatch for $asset (want $want, got $got)" }
      Write-Host 'Checksum OK.'
    }
  } catch [System.Net.WebException] {
    # no checksums.txt on this release; continue without verification
  }

  # extract + hand off to the binary's own installer
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $exe = Join-Path $tmp 'gkebnkctl.exe'
  if (-not (Test-Path $exe)) { throw "gkebnkctl: gkebnkctl.exe not found in $asset" }

  Write-Host "Installing via 'gkebnkctl install'..."
  # Judge the native binary by its exit code, not native-stderr text.
  $prev = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  if ($InstallArgs) {
    & $exe install --force $InstallArgs.Split(' ') 2>&1 | ForEach-Object { Write-Host $_ }
  } else {
    & $exe install --force 2>&1 | ForEach-Object { Write-Host $_ }
  }
  $code = $LASTEXITCODE
  $ErrorActionPreference = $prev
  if ($code -ne 0) { throw "gkebnkctl install failed (exit $code)" }
}
finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host "Done. Run 'gkebnkctl version' to confirm."
