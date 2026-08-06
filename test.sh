#!/bin/sh
# Run the full test suite
set -e

cd "$(dirname "$0")"
go test ./...
sh scripts/test-shadow.sh
sh scripts/test-build-version.sh
sh scripts/test-install-guard.sh

# scripts/test-install.sh is deliberately NOT here: it downloads a real release
# from GitHub, so it runs in CI's `install` job instead. Everything about the
# newer-mg guard that can be tested without the network is in
# test-install-guard.sh above, and that is where guard cases belong.
