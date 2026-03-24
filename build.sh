#!/bin/sh
# Build and install the mg binary to $GOPATH/bin
set -e

cd "$(dirname "$0")"

# Check formatting
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
    echo "Error: the following files are not formatted with gofmt:"
    echo "$unformatted"
    echo ""
    echo "Run: gofmt -w ."
    exit 1
fi

go install ./cmd/mg
echo "Installed: $(go env GOPATH)/bin/mg"
