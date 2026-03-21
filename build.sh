#!/bin/sh
# Build and install the mg binary to $GOPATH/bin
set -e

cd "$(dirname "$0")"
go install ./cmd/mg
echo "Installed: $(go env GOPATH)/bin/mg"
