#!/usr/bin/env bash
# Each binary must embed a tunnel client for its own target architecture.
set -euo pipefail
cd "$(dirname "$0")/.."
version="${VERSION:-$(git describe --tags --always)}"
payload=server/http_tunnel_client_bin/http-tunnel-client
backup="$(mktemp -d)"
if [[ -f "$payload" ]]; then cp -p "$payload" "$backup/client"; fi
cleanup() {
  if [[ -f "$backup/client" ]]; then cp -p "$backup/client" "$payload";
  else rm -f "$payload"; fi
  rm -rf "$backup"
}
trap cleanup EXIT
if [[ "$#" -eq 0 ]]; then set -- amd64 arm64; fi
for arch in "$@"; do
  case "$arch" in amd64|arm64) ;; *) echo "Unsupported architecture: $arch" >&2; exit 1 ;; esac
  ./scripts/prepare-http-tunnel-client.sh "$arch"
  mkdir -p "builds/pkg/linux_${arch}"
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X main.Version=${version}" -o "builds/pkg/linux_${arch}/gotty" .
done
