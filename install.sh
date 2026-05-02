#!/bin/sh
# Install macguffin binary from the latest GitHub release.
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/drellem2/macguffin/main/install.sh | sh
#   sh install.sh              # install to ~/.local/bin
#   INSTALL_DIR=/usr/local/bin sh install.sh
#
# Environment:
#   INSTALL_DIR   Where to put the mg binary (default: ~/.local/bin).
#   SHADOW_DIR    Directory for the shadow symlink that hides /usr/bin/mg
#                 (microemacs) on macOS/Linux. Default: /usr/local/bin.
#   SHADOW_MG     Set to 0 to skip the shadow symlink. Default: 1.
#
# Supports: Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD (amd64)

set -e

REPO="drellem2/macguffin"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
SHADOW_DIR="${SHADOW_DIR:-/usr/local/bin}"
SHADOW_MG="${SHADOW_MG:-1}"

detect_os() {
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$os" in
        linux)   echo "linux" ;;
        darwin)  echo "darwin" ;;
        freebsd) echo "freebsd" ;;
        *)
            echo "Unsupported OS: $os" >&2
            exit 1
            ;;
    esac
}

detect_arch() {
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64)  echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *)
            echo "Unsupported architecture: $arch" >&2
            exit 1
            ;;
    esac
}

get_latest_version() {
    curl -sSfL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed 's/.*"tag_name": *"//;s/".*//'
}

# Absolute path of $1, even if the directory doesn't exist yet.
abspath() {
    case "$1" in
        /*) echo "$1" ;;
        *)  echo "$PWD/$1" ;;
    esac
}

warn_system_mg() {
    # macOS ships /usr/bin/mg as microemacs (a tiny Emacs clone). Some Linux
    # distros also have it via the `mg` package. Either way, if it's there,
    # tell the user we're going to shadow it.
    [ -e /usr/bin/mg ] || return 0
    echo "" >&2
    echo "NOTE: /usr/bin/mg already exists (typically the microemacs editor)." >&2
    if [ "$SHADOW_MG" = "0" ]; then
        echo "Shadowing is disabled (SHADOW_MG=0). 'mg' may continue to resolve" >&2
        echo "to /usr/bin/mg unless ${INSTALL_DIR} precedes /usr/bin in your PATH." >&2
    else
        echo "macguffin will install at ${INSTALL_DIR}/mg and shadow it via" >&2
        echo "${SHADOW_DIR}/mg, which precedes /usr/bin in the default PATH." >&2
    fi
    echo "" >&2
}

shadow_link() {
    target="$1"
    os="$2"

    [ "$SHADOW_MG" = "0" ] && return 0

    # Shadow only applies on platforms where SHADOW_DIR precedes /usr/bin in
    # the default PATH. macOS and Linux both put /usr/local/bin first.
    case "$os" in
        darwin|linux) ;;
        *) return 0 ;;
    esac

    # If the binary itself lives in SHADOW_DIR, there's nothing to symlink.
    case "$target" in
        "${SHADOW_DIR}/mg") return 0 ;;
    esac

    link="${SHADOW_DIR}/mg"

    # Already pointing to us? Done.
    if [ -L "$link" ] && [ "$(readlink "$link")" = "$target" ]; then
        echo "Shadow symlink already in place: ${link} -> ${target}"
        return 0
    fi

    # Something non-symlink (a real file) lives there. Don't clobber it —
    # could be a Homebrew install of macguffin, or another tool.
    if [ -e "$link" ] && ! [ -L "$link" ]; then
        echo "Warning: ${link} exists and is not a symlink; skipping shadow." >&2
        echo "  Remove it manually if you want install.sh to manage it," >&2
        echo "  or set SHADOW_MG=0 to suppress this step." >&2
        return 0
    fi

    SUDO=""
    if [ ! -d "$SHADOW_DIR" ] || [ ! -w "$SHADOW_DIR" ]; then
        if command -v sudo >/dev/null 2>&1; then
            SUDO="sudo"
            echo "Creating ${link} (requires sudo to write to ${SHADOW_DIR})..."
        else
            echo "Warning: ${SHADOW_DIR} not writable and sudo unavailable; skipping shadow." >&2
            echo "  Set SHADOW_DIR to a writable directory, or SHADOW_MG=0 to suppress." >&2
            return 0
        fi
        if [ ! -d "$SHADOW_DIR" ]; then
            $SUDO mkdir -p "$SHADOW_DIR"
        fi
    fi

    $SUDO ln -sf "$target" "$link"
    echo "Linked ${link} -> ${target}"
}

main() {
    os="$(detect_os)"
    arch="$(detect_arch)"
    platform="${os}_${arch}"
    version="$(get_latest_version)"

    if [ -z "$version" ]; then
        echo "Error: could not determine latest version" >&2
        exit 1
    fi

    warn_system_mg

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

    target="$(abspath "${INSTALL_DIR}")/mg"
    echo "Installed mg to ${target}"

    shadow_link "$target" "$os"

    # Check if INSTALL_DIR is in PATH (only relevant when shadow didn't take).
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            if [ "$SHADOW_MG" = "0" ] || ! { [ "$os" = "darwin" ] || [ "$os" = "linux" ]; }; then
                echo ""
                echo "NOTE: ${INSTALL_DIR} is not in your PATH."
                echo "Add it:  export PATH=\"${INSTALL_DIR}:\$PATH\""
            fi
            ;;
    esac
}

# Skip main when this script is sourced for testing (e.g. by scripts/test-shadow.sh).
[ -n "${MG_INSTALL_SKIP_MAIN:-}" ] || main "$@"
