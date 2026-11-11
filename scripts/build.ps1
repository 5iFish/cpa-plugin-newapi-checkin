param([string]$Go='go',[string]$CC=$(if ($env:CC) { $env:CC } else { 'gcc' }),[string]$Output='')
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
 $target = @(& $Go env GOHOSTOS GOHOSTARCH GOOS GOARCH)
 if($LASTEXITCODE -ne 0){throw 'Cannot inspect Go build target'}
 if($target.Count -ne 4 -or $target[0] -ne 'windows' -or $target[1] -notin @('amd64','arm64') -or $target[2] -ne $target[0] -or $target[3] -ne $target[1]){
  throw 'This script requires native Windows amd64 or arm64; unset incompatible GOOS/GOARCH overrides.'
 }
 $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
 $expectedOSArch = if($target[1] -eq 'amd64'){'x64'}else{'arm64'}
 if($osArch -ne $expectedOSArch){throw 'Go toolchain architecture must match the native Windows OS architecture.'}
 if(-not $Output){$Output="dist/windows-$($target[1])/newapi-checkin.dll"}
 $env:CGO_ENABLED='1'; $env:CC=$CC; $env:GOWORK='off'; $env:GOMAXPROCS='2'
 & $Go version
 & $CC --version | Select-Object -First 1
 New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
 & $Go build -p=2 -trimpath -buildmode=c-shared -o $Output .
 if($LASTEXITCODE -ne 0){throw 'Shared library build failed'}
 Get-FileHash -Algorithm SHA256 -LiteralPath $Output
} finally {Pop-Location}
