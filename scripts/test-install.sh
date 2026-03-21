#!/bin/sh
# Test that install.sh works from scratch (no prior mg binary).
# This exercises the full install flow into a temporary directory.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SCRIPT="${SCRIPT_DIR}/install.sh"

echo "=== Testing install.sh from scratch ==="

# Use a fresh temp directory — no prior mg binary
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

export INSTALL_DIR="${tmpdir}/bin"

echo "1. Install directory does not exist yet:"
test ! -d "$INSTALL_DIR" && echo "   PASS: ${INSTALL_DIR} absent"

echo "2. Running install.sh..."
sh "$INSTALL_SCRIPT"

echo "3. Binary exists and is executable:"
test -x "${INSTALL_DIR}/mg" && echo "   PASS: ${INSTALL_DIR}/mg is executable"

echo "4. Binary runs and reports version:"
version_output="$("${INSTALL_DIR}/mg" version)"
echo "   ${version_output}"
echo "$version_output" | grep -q "^mg v" && echo "   PASS: version string looks correct"

echo ""
echo "=== All install tests passed ==="
