#!/bin/sh
# Remove macguffin (mg) binary and shadow symlink installed by install.sh.
#
# Usage:
#   sh uninstall.sh
#   INSTALL_DIR=/usr/local/bin sh uninstall.sh
#
# Environment:
#   INSTALL_DIR   Where mg was installed (default: ~/.local/bin).
#   SHADOW_DIR    Where the shadow symlink lives (default: /usr/local/bin).

set -e

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
SHADOW_DIR="${SHADOW_DIR:-/usr/local/bin}"

abspath() {
    case "$1" in
        /*) echo "$1" ;;
        *)  echo "$PWD/$1" ;;
    esac
}

binary="$(abspath "$INSTALL_DIR")/mg"
shadow="${SHADOW_DIR}/mg"

removed_any=0

# Remove shadow symlink only if it points to our installed binary —
# never blow away a real file or a foreign symlink.
if [ -L "$shadow" ]; then
    target="$(readlink "$shadow")"
    case "$target" in
        "$binary"|"$INSTALL_DIR"/*|"$(abspath "$INSTALL_DIR")"/*)
            SUDO=""
            if [ ! -w "$SHADOW_DIR" ]; then
                if command -v sudo >/dev/null 2>&1; then
                    SUDO="sudo"
                    echo "Removing ${shadow} (requires sudo)..."
                else
                    echo "Error: ${SHADOW_DIR} not writable and sudo unavailable." >&2
                    echo "  Remove ${shadow} manually." >&2
                    exit 1
                fi
            fi
            $SUDO rm -f "$shadow"
            echo "Removed shadow symlink ${shadow}"
            removed_any=1
            ;;
        *)
            echo "Skipping ${shadow}: symlink target ${target} is not from this install."
            ;;
    esac
elif [ -e "$shadow" ]; then
    echo "Skipping ${shadow}: not a symlink (left in place)."
fi

if [ -e "$binary" ]; then
    rm -f "$binary"
    echo "Removed ${binary}"
    removed_any=1
fi

if [ "$removed_any" = "0" ]; then
    echo "Nothing to remove. Looked for ${binary} and ${shadow}."
fi
