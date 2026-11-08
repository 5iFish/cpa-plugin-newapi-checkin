param([string]$Go='go',[string]$CC='gcc',[string]$Output='dist/windows-amd64/newapi-checkin.dll')
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
 $target = @(& $Go env GOHOSTOS GOHOSTARCH GOOS GOARCH)
 if($LASTEXITCODE -ne 0){throw 'Cannot inspect Go build target'}
 if($target.Count -ne 4 -or $target[0] -ne 'windows' -or $target[1] -ne 'amd64' -or $target[2] -ne 'windows' -or $target[3] -ne 'amd64'){
  throw 'This script requires native Windows amd64; unset incompatible GOOS/GOARCH overrides.'
 }
 $env:CGO_ENABLED='1'; $env:CC=$CC; $env:GOWORK='off'; $env:GOMAXPROCS='2'
 & $Go version
 & $CC --version | Select-Object -First 1
 New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
 & $Go build -p=2 -trimpath -buildmode=c-shared -o $Output .
 if($LASTEXITCODE -ne 0){throw 'Shared library build failed'}
 Get-FileHash -Algorithm SHA256 -LiteralPath $Output
} finally {Pop-Location}
