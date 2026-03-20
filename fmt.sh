#!/bin/sh
# Check formatting with gofmt
set -e

cd "$(dirname "$0")"

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
    echo "Files need formatting:"
    echo "$unformatted"
    echo ""
    echo "Run: gofmt -w ."
    exit 1
fi

echo "All files formatted correctly."
