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

    url="https://github.com/${REPO}/releases/download/${version}/mg_${platform}"
    echo "Downloading mg ${version} for ${platform}..."

    mkdir -p "$INSTALL_DIR"
    curl -sSfL "$url" -o "${INSTALL_DIR}/mg"
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
