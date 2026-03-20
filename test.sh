#!/bin/sh
# Run the full test suite
set -e

cd "$(dirname "$0")"
go test ./...
