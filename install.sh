#!/bin/sh
# Install macguffin binary from the latest GitHub release.
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/drellem2/macguffin/main/install.sh | sh
#   sh install.sh              # install to ~/.local/bin
#   INSTALL_DIR=/usr/local/bin sh install.sh
#
# Supports: Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD (amd64)

set -e

REPO="drellem2/macguffin"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

detect_platform() {
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        linux)  os="linux" ;;
        darwin) os="darwin" ;;
        freebsd) os="freebsd" ;;
        *)
            echo "Unsupported OS: $os" >&2
            exit 1
            ;;
    esac

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)
            echo "Unsupported architecture: $arch" >&2
            exit 1
            ;;
    esac

    echo "${os}_${arch}"
}

get_latest_version() {
    curl -sSfL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed 's/.*"tag_name": *"//;s/".*//'
}

main() {
    platform="$(detect_platform)"
    version="$(get_latest_version)"

    if [ -z "$version" ]; then
        echo "Error: could not determine latest version" >&2
        exit 1
    fi

    base_url="https://github.com/${REPO}/releases/download/${version}"
    binary="mg_${platform}"
    echo "Downloading mg ${version} for ${platform}..."

    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT

    curl -sSfL "${base_url}/${binary}" -o "${tmpdir}/${binary}"
    curl -sSfL "${base_url}/checksums.txt" -o "${tmpdir}/checksums.txt"

    # Verify checksum
    expected="$(grep "${binary}" "${tmpdir}/checksums.txt" | awk '{print $1}')"
    if [ -z "$expected" ]; then
        echo "Error: no checksum found for ${binary}" >&2
        exit 1
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "${tmpdir}/${binary}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "${tmpdir}/${binary}" | awk '{print $1}')"
    else
        echo "Warning: no sha256 tool found, skipping checksum verification" >&2
        actual="$expected"
    fi

    if [ "$actual" != "$expected" ]; then
        echo "Error: checksum mismatch (expected ${expected}, got ${actual})" >&2
        exit 1
    fi

    mkdir -p "$INSTALL_DIR"
    mv "${tmpdir}/${binary}" "${INSTALL_DIR}/mg"
    chmod +x "${INSTALL_DIR}/mg"

    echo "Installed mg to ${INSTALL_DIR}/mg"

    # Check if INSTALL_DIR is in PATH
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            echo ""
            echo "NOTE: ${INSTALL_DIR} is not in your PATH."
            echo "Add it:  export PATH=\"${INSTALL_DIR}:\$PATH\""
            ;;
    esac
}

main
