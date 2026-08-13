#!/bin/sh
# Test install.sh end to end: a real download of the latest release into a
# temporary directory, then the newer-mg guard driven through main() rather
# than by calling its functions directly (scripts/test-install-guard.sh does
# that, hermetically, and is the suite to add cases to unless the case genuinely
# needs the download).
#
# This one needs the network, so it lives in CI's `install` job rather than in
# ./test.sh.
#
# Two things about this file were broken and are worth not reintroducing:
#
#   1. Every assertion was written `test ... && echo "   PASS"`. Under `set -e` a
#      failing AND-list is not an error, so a false assertion printed nothing and
#      the script went on to announce "=== All install tests passed ===" and exit
#      0. The `grep -q "^mg v"` check had in fact stopped matching — releases
#      print `mg 0.3.0`, because goreleaser stamps {{.Version}} without the `v` —
#      and nobody could tell. Assertions here go through fail().
#
#   2. It ran the real install.sh with the shadow step enabled, so it repointed
#      the host's /usr/local/bin/mg at a temp directory and then deleted the
#      directory, leaving a dangling symlink on whatever machine ran it. Every
#      install here sets SHADOW_MG=0.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SCRIPT="${SCRIPT_DIR}/install.sh"

echo "=== Testing install.sh end to end ==="

# The swept root, not the shared $TMPDIR — see scripts/lib/testtmp.sh. The trap
# below is still what reclaims the space on the ordinary path; what the helper
# adds is the reclaim that does not depend on it, because a trap does not fire on
# SIGKILL and this one did not name TERM/INT/HUP either (mg-cc3f).
# shellcheck source=lib/testtmp.sh
. "${SCRIPT_DIR}/scripts/lib/testtmp.sh"
tmpdir="$(testtmp_dir test-install)" || exit 1
trap 'testtmp_remove "$tmpdir"' EXIT INT TERM HUP

INSTALL_DIR="${tmpdir}/bin"
export INSTALL_DIR

# SHADOW_MG=0: see (2) above. This must never touch the host's /usr/local/bin.
SHADOW_MG=0
export SHADOW_MG

# A hermetic PATH with INSTALL_DIR on it, which is how a real install is meant
# to end up. Without this, `command -v mg` finds the developer's own source
# build — which is newer than the latest release, so the guard correctly refuses
# and the test fails for a reason that has nothing to do with the test.
PATH="${INSTALL_DIR}:/usr/bin:/bin"
export PATH

fail() { echo "FAIL: $*" >&2; exit 1; }

# A stand-in mg reporting $2, formatted as `mg version` prints it.
mkmg() {
    cat > "$1" <<EOF
#!/bin/sh
[ "\$1" = version ] || exit 1
echo "mg $2 (abc1234, 2026-08-06)"
EOF
    chmod +x "$1"
}

echo "1. Install directory does not exist yet:"
[ ! -d "$INSTALL_DIR" ] || fail "${INSTALL_DIR} already exists"
echo "   PASS: ${INSTALL_DIR} absent"

echo "2. Running install.sh..."
sh "$INSTALL_SCRIPT"

echo "3. Binary exists and is executable:"
[ -x "${INSTALL_DIR}/mg" ] || fail "${INSTALL_DIR}/mg is not executable"
echo "   PASS: ${INSTALL_DIR}/mg is executable"

echo "4. Binary runs and reports version:"
version_output="$("${INSTALL_DIR}/mg" version)"
echo "   ${version_output}"
# Releases print `mg 0.3.0 (sha, date)`; a source build would print `mg v0.3.1-dev.N+gsha`.
echo "$version_output" | grep -Eq "^mg v?[0-9]+\.[0-9]+\.[0-9]+" \
    || fail "version string does not look like a version: ${version_output}"
echo "   PASS: version string looks correct"

echo "5. Reinstalling over the same version proceeds:"
sh "$INSTALL_SCRIPT" || fail "reinstall of an identical version should not be refused"
echo "   PASS"

echo "6. POSITIVE CONTROL — a NEWER installed mg makes install.sh REFUSE:"
mkmg "${INSTALL_DIR}/mg" v99.0.0
if sh "$INSTALL_SCRIPT" >"${tmpdir}/out" 2>"${tmpdir}/err"; then
    fail "install.sh proceeded over a newer mg: $(cat "${tmpdir}/err")"
fi
grep -q "refusing to install" "${tmpdir}/err" || fail "no refusal message: $(cat "${tmpdir}/err")"
grep -q "v99.0.0" "${tmpdir}/err" || fail "refusal does not name the installed version"
# It refused BEFORE downloading, and the newer binary is untouched.
if grep -q "Downloading" "${tmpdir}/out"; then fail "refused install should not have downloaded"; fi
"${INSTALL_DIR}/mg" version | grep -q "v99.0.0" || fail "the newer binary was overwritten anyway"
echo "   PASS: refused, downloaded nothing, left the binary alone"

echo "7. THE OVERRIDE — --force installs over it:"
sh "$INSTALL_SCRIPT" --force >"${tmpdir}/out" 2>"${tmpdir}/err" \
    || fail "--force did not install: $(cat "${tmpdir}/err")"
grep -q "because --force was given" "${tmpdir}/err" || fail "forced downgrade was not announced"
if "${INSTALL_DIR}/mg" version | grep -q "v99.0.0"; then
    fail "--force did not actually replace the newer binary"
fi
echo "   PASS: overrode the refusal and replaced the binary"

echo "8. THE OVERRIDE — MG_FORCE=1 does the same:"
mkmg "${INSTALL_DIR}/mg" v99.0.0
MG_FORCE=1 sh "$INSTALL_SCRIPT" >"${tmpdir}/out" 2>"${tmpdir}/err" \
    || fail "MG_FORCE=1 did not install: $(cat "${tmpdir}/err")"
if "${INSTALL_DIR}/mg" version | grep -q "v99.0.0"; then
    fail "MG_FORCE=1 did not actually replace the newer binary"
fi
echo "   PASS"

echo "9. POSITIVE CONTROL — an OLDER installed mg lets install.sh PROCEED:"
mkmg "${INSTALL_DIR}/mg" v0.0.1
sh "$INSTALL_SCRIPT" >"${tmpdir}/out" 2>"${tmpdir}/err" \
    || fail "install.sh refused an older mg: $(cat "${tmpdir}/err")"
if "${INSTALL_DIR}/mg" version | grep -q "v0.0.1"; then fail "the older binary was not replaced"; fi
echo "   PASS: proceeded and replaced it"

echo "10. An UNPARSEABLE installed version warns but does not block:"
mkmg "${INSTALL_DIR}/mg" dev
sh "$INSTALL_SCRIPT" >"${tmpdir}/out" 2>"${tmpdir}/err" \
    || fail "install.sh refused on an unparseable version: $(cat "${tmpdir}/err")"
grep -q "cannot be compared" "${tmpdir}/err" || fail "no warning for the 'dev' binary"
if "${INSTALL_DIR}/mg" version | grep -q "^mg dev"; then fail "the 'dev' binary was not replaced"; fi
echo "   PASS: warned and installed"

echo "11. The host's shadow symlink was left alone:"
# The bug this file used to have. Nothing under SHADOW_DIR should have been touched.
[ ! -L /usr/local/bin/mg ] || [ -e /usr/local/bin/mg ] \
    || fail "/usr/local/bin/mg is now a DANGLING symlink — SHADOW_MG=0 did not hold"
echo "   PASS"

echo ""
echo "=== All install tests passed ==="
