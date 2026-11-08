#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
: "${CC:=gcc}"
: "${GOMAXPROCS:=2}"
export CC GOMAXPROCS
export CGO_ENABLED=1 GOWORK=off
host_os="$(go env GOHOSTOS)"
host_arch="$(go env GOHOSTARCH)"
target_os="$(go env GOOS)"
target_arch="$(go env GOARCH)"
if [ "$host_os" != linux ] || [ "$target_os" != linux ] || [ "$target_arch" != "$host_arch" ]; then
  printf '%s\n' 'This script requires native Linux; unset incompatible GOOS/GOARCH overrides.' >&2
  exit 1
fi
go version
"$CC" --version
case "$(ldd --version 2>&1)" in
  *musl*) printf '%s\n' 'Unsupported runtime: this CPA dynamic library requires glibc Linux; musl loading failed validation.' >&2; exit 1 ;;
esac
arch="$host_arch"
case "$arch" in
  amd64|arm64) ;;
  *) printf 'Unsupported Linux architecture: %s\n' "$arch" >&2; exit 1 ;;
esac
target="${1:-dist/linux-$arch/newapi-checkin.so}"
mkdir -p "$(dirname "$target")"
go build -p=2 -trimpath -buildmode=c-shared -o "$target" .
sha256sum "$target"
