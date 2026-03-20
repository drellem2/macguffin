#!/bin/sh
# Build the mg binary
set -e

cd "$(dirname "$0")"
go build -o mg ./cmd/mg
echo "Built: ./mg"
