#!/bin/bash
set -ex

rm -f -v ./gotty
cd js
[ ! -d "node_modules" ] && npm install
npx webpack --mode=production
cd ..
go build
