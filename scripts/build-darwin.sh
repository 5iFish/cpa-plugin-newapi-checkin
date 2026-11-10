#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
: "${CC:=clang}"
: "${GOMAXPROCS:=2}"
: "${MACOSX_DEPLOYMENT_TARGET:=15.0}"
export CC GOMAXPROCS MACOSX_DEPLOYMENT_TARGET
export CGO_ENABLED=1 GOWORK=off
host_os="$(go env GOHOSTOS)"
host_arch="$(go env GOHOSTARCH)"
target_os="$(go env GOOS)"
target_arch="$(go env GOARCH)"
if [ "$host_os" != darwin ] || [ "$target_os" != darwin ] || [ "$target_arch" != "$host_arch" ]; then
  printf '%s\n' 'This script requires native macOS; unset incompatible GOOS/GOARCH overrides.' >&2
  exit 1
fi
case "$host_arch" in
  amd64|arm64) ;;
  *) printf 'Unsupported macOS architecture: %s\n' "$host_arch" >&2; exit 1 ;;
esac
go version
"$CC" --version
target="${1:-dist/darwin-$host_arch/newapi-checkin.dylib}"
mkdir -p "$(dirname "$target")"
go build -p=2 -trimpath -buildmode=c-shared -o "$target" .
shasum -a 256 "$target"
