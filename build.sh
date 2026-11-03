#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
npm --prefix js ci
make typecheck assets
go build
