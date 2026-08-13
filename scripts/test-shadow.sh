#!/bin/sh
# Test the shadow-symlink logic in install.sh and the uninstall flow,
# without doing a network install. Sources install.sh with MG_INSTALL_SKIP_MAIN
# so we can call individual functions directly.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SCRIPT="${SCRIPT_DIR}/install.sh"
UNINSTALL_SCRIPT="${SCRIPT_DIR}/uninstall.sh"

# The swept root, not the shared $TMPDIR — see scripts/lib/testtmp.sh. The trap
# below is still what reclaims the space on the ordinary path; what the helper
# adds is the reclaim that does not depend on it, because a trap does not fire on
# SIGKILL and this one did not name TERM/INT/HUP either (mg-cc3f).
# shellcheck source=lib/testtmp.sh
. "${SCRIPT_DIR}/scripts/lib/testtmp.sh"
tmpdir="$(testtmp_dir test-shadow)" || exit 1
trap 'testtmp_remove "$tmpdir"' EXIT INT TERM HUP

INSTALL_DIR="${tmpdir}/bin"
SHADOW_DIR="${tmpdir}/shadow"
mkdir -p "$INSTALL_DIR" "$SHADOW_DIR"

# Stand in for the real macguffin binary.
cat > "${INSTALL_DIR}/mg" <<'EOF'
#!/bin/sh
echo "mg vTEST.0.0"
EOF
chmod +x "${INSTALL_DIR}/mg"

export INSTALL_DIR SHADOW_DIR
export MG_INSTALL_SKIP_MAIN=1

# shellcheck disable=SC1090
. "$INSTALL_SCRIPT"

fail() { echo "FAIL: $*" >&2; exit 1; }

echo "=== Test: shadow_link creates symlink on darwin ==="
shadow_link "${INSTALL_DIR}/mg" darwin
[ -L "${SHADOW_DIR}/mg" ] || fail "symlink not created"
[ "$(readlink "${SHADOW_DIR}/mg")" = "${INSTALL_DIR}/mg" ] || fail "wrong symlink target"
echo "  PASS"

echo "=== Test: shadow_link creates symlink on linux ==="
rm -f "${SHADOW_DIR}/mg"
shadow_link "${INSTALL_DIR}/mg" linux
[ -L "${SHADOW_DIR}/mg" ] || fail "symlink not created on linux"
echo "  PASS"

echo "=== Test: shadow_link is idempotent ==="
shadow_link "${INSTALL_DIR}/mg" darwin >/dev/null
[ -L "${SHADOW_DIR}/mg" ] || fail "symlink missing after second call"
echo "  PASS"

echo "=== Test: shadow_link skipped on freebsd ==="
rm -f "${SHADOW_DIR}/mg"
shadow_link "${INSTALL_DIR}/mg" freebsd
[ ! -e "${SHADOW_DIR}/mg" ] || fail "symlink should not be created on freebsd"
echo "  PASS"

echo "=== Test: shadow_link skipped when SHADOW_MG=0 ==="
saved_shadow_mg="$SHADOW_MG"
SHADOW_MG=0
shadow_link "${INSTALL_DIR}/mg" linux
[ ! -e "${SHADOW_DIR}/mg" ] || fail "SHADOW_MG=0 should disable shadow"
SHADOW_MG="$saved_shadow_mg"
echo "  PASS"

echo "=== Test: shadow_link refuses to clobber a regular file ==="
echo "preexisting" > "${SHADOW_DIR}/mg"
shadow_link "${INSTALL_DIR}/mg" darwin 2>/dev/null || true
[ ! -L "${SHADOW_DIR}/mg" ] || fail "should not have replaced regular file with symlink"
[ "$(cat "${SHADOW_DIR}/mg")" = "preexisting" ] || fail "regular file contents changed"
echo "  PASS"
rm -f "${SHADOW_DIR}/mg"

echo "=== Test: shadow_link no-ops when target already lives in SHADOW_DIR ==="
mkdir -p "${tmpdir}/colocated"
cat > "${tmpdir}/colocated/mg" <<'EOF'
#!/bin/sh
echo colocated
EOF
chmod +x "${tmpdir}/colocated/mg"
saved_shadow="$SHADOW_DIR"
SHADOW_DIR="${tmpdir}/colocated"
shadow_link "${tmpdir}/colocated/mg" darwin
[ ! -L "${tmpdir}/colocated/mg" ] || fail "should not turn binary into symlink to itself"
[ -f "${tmpdir}/colocated/mg" ] || fail "binary should still be a regular file"
SHADOW_DIR="$saved_shadow"
echo "  PASS"

echo "=== Test: warn_system_mg mentions microemacs when /usr/bin/mg exists ==="
if [ -e /usr/bin/mg ]; then
    out="$(warn_system_mg 2>&1 1>/dev/null)"
    case "$out" in
        *microemacs*) echo "  PASS" ;;
        *) fail "warning text missing 'microemacs': $out" ;;
    esac
    case "$out" in
        *"${SHADOW_DIR}/mg"*) ;;
        *) fail "warning should reference SHADOW_DIR target: $out" ;;
    esac
else
    echo "  SKIP: /usr/bin/mg not present on this system"
fi

echo "=== Test: uninstall.sh removes shadow + binary ==="
ln -sf "${INSTALL_DIR}/mg" "${SHADOW_DIR}/mg"
INSTALL_DIR="$INSTALL_DIR" SHADOW_DIR="$SHADOW_DIR" sh "$UNINSTALL_SCRIPT" >/dev/null
[ ! -e "${INSTALL_DIR}/mg" ] || fail "binary still present after uninstall"
[ ! -e "${SHADOW_DIR}/mg" ] || fail "shadow symlink still present after uninstall"
echo "  PASS"

echo "=== Test: uninstall.sh leaves foreign symlinks alone ==="
mkdir -p "$INSTALL_DIR"
echo '#!/bin/sh' > "${INSTALL_DIR}/mg"
chmod +x "${INSTALL_DIR}/mg"
ln -sf /tmp/some-other-mg "${SHADOW_DIR}/mg"
INSTALL_DIR="$INSTALL_DIR" SHADOW_DIR="$SHADOW_DIR" sh "$UNINSTALL_SCRIPT" >/dev/null
[ -L "${SHADOW_DIR}/mg" ] || fail "foreign symlink should remain"
[ "$(readlink "${SHADOW_DIR}/mg")" = "/tmp/some-other-mg" ] || fail "foreign symlink target altered"
rm -f "${SHADOW_DIR}/mg"
echo "  PASS"

echo ""
echo "=== All shadow tests passed ==="
