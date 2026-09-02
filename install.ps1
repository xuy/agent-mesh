# Install the mesh binary on Windows.
#
#   irm https://raw.githubusercontent.com/xuy/agent-mesh/main/install.ps1 | iex
#
# Downloads the latest release for this architecture, checks that it runs, puts
# it in %LOCALAPPDATA%\Programs\mesh and on the user PATH.
#
# Saved to a file, it also takes -Join, which joins a mesh and registers it with
# the agents installed here:
#
#   .\install.ps1 -Join '--name windows --lan --code M5TQ6692'
#
# Set MESH_INSTALL_DIR to install somewhere else.

param([string]$Join = '')

$ErrorActionPreference = 'Stop'

$repo = 'xuy/agent-mesh'
$dir = if ($env:MESH_INSTALL_DIR) { $env:MESH_INSTALL_DIR } else { "$env:LOCALAPPDATA\Programs\mesh" }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "mesh: no build for $env:PROCESSOR_ARCHITECTURE -- build from source with: go install github.com/$repo/cmd/mesh@latest" }
}

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$tag = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
if (-not $tag) { throw 'mesh: could not find the latest release' }

$url = "https://github.com/$repo/releases/download/$tag/mesh-windows-$arch.exe"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("mesh-" + [Guid]::NewGuid().ToString('N') + ".exe")

Write-Host "downloading mesh $tag for windows/$arch"
try {
    Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing
} catch {
    throw "mesh: $url is not available"
}

# Prove it runs before putting it on PATH, so a bad download fails here rather
# than the first time someone needs it. Also clears the mark-of-the-web, which
# otherwise makes SmartScreen block the first run with no useful message.
Unblock-File -Path $tmp -ErrorAction SilentlyContinue
& $tmp version *>$null
if ($LASTEXITCODE -ne 0) { Remove-Item $tmp -Force; throw 'mesh: the downloaded binary does not run' }

New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'mesh.exe'

# A running daemon holds its own image open, so overwriting it fails with
# "being used by another process". Stop it first; it is restarted below.
$running = $false
if (Test-Path $exe) {
    Get-Process -Name mesh -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } | ForEach-Object {
        $running = $true
        & $exe down *>$null
    }
}
Move-Item -Path $tmp -Destination $exe -Force
Write-Host "installed $exe"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
    Write-Host "added $dir to your PATH (open a new terminal to pick it up)"
}
$env:Path = "$env:Path;$dir"

if ($Join) {
    Write-Host ''
    & $exe join @($Join -split '\s+' | Where-Object { $_ })
    Write-Host ''
    & $exe connect
    Write-Host ''
    Write-Host 'Next:  mesh service install    # keep this node up across reboots'
} elseif ($running) {
    & $exe up *>$null
    Write-Host 'restarted the running daemon on the new build'
} else {
    Write-Host ''
    Write-Host "Next:  mesh join --name $env:COMPUTERNAME"
}
