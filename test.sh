#!/bin/sh
# Run the full test suite
set -e

cd "$(dirname "$0")"
go test ./...
sh scripts/test-shadow.sh
sh scripts/test-build-version.sh
