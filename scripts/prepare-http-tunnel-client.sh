#!/usr/bin/env bash
set -euo pipefail

host_machine="$(uname -m)"
case "${host_machine}" in
  x86_64) host_arch="amd64" ;;
  aarch64|arm64) host_arch="arm64" ;;
  *) host_arch="" ;;
esac

arch="${1:-${GOARCH:-${host_arch}}}"
if [[ -z "${arch}" ]]; then
  echo "unsupported architecture: ${host_machine}" >&2
  exit 1
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
if [[ "${arch}" == "${host_arch}" ]]; then
  "${tmp}" version >/dev/null
else
  if [[ ! -s "${tmp}" ]] || [[ "$(head -c 4 "${tmp}")" != $'\x7fELF' ]]; then
    echo "downloaded http-tunnel-client for ${arch} is not a valid ELF binary" >&2
    exit 1
  fi
fi
mv "${tmp}" "${out}"
