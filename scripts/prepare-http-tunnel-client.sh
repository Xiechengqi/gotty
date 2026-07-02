#!/usr/bin/env bash
set -euo pipefail

arch="${1:-${GOARCH:-}}"
if [[ -z "${arch}" ]]; then
  machine="$(uname -m)"
  case "${machine}" in
    x86_64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "unsupported architecture: ${machine}" >&2; exit 1 ;;
  esac
fi

case "${arch}" in
  amd64)
    url="https://github.com/Xiechengqi/http-tunnel/releases/download/latest/http-tunnel-client-linux-amd64"
    ;;
  arm64)
    url="https://github.com/Xiechengqi/http-tunnel/releases/download/latest/http-tunnel-client-linux-arm64"
    ;;
  *)
    echo "unsupported architecture: ${arch}" >&2
    exit 1
    ;;
esac

out="server/http_tunnel_client_bin/http-tunnel-client"
tmp="${out}.tmp"
mkdir -p "$(dirname "${out}")"
curl -fsSL "${url}" -o "${tmp}"
chmod 0755 "${tmp}"
"${tmp}" version >/dev/null
mv "${tmp}" "${out}"
