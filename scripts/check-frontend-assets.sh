#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
changed="$(git status --porcelain -- bindata/static)"
if [[ -n "$changed" ]]; then
  echo 'Frontend assets differ from the committed files. Run make assets and commit the result.' >&2
  echo "$changed" >&2
  git diff --stat -- bindata/static
  exit 1
fi
# Ensure module concatenation or plugin changes cannot silently lose licenses.
for package in xterm addon-fit addon-image addon-unicode11 addon-web-links addon-webgl; do
  if ! grep -Fq "@xterm/${package}" bindata/static/js/gotty.licenses.txt; then
    echo "Missing @xterm/${package} in the frontend license inventory" >&2
    exit 1
  fi
done
