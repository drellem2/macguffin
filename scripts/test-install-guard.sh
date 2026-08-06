#!/bin/sh
# Test install.sh's refusal to overwrite a newer mg, without a network install.
# Sources install.sh with MG_INSTALL_SKIP_MAIN so the functions can be called
# directly, in the same pattern as scripts/test-shadow.sh.
#
# The property under test is that the guard CAN FIRE, in both directions and on
# BOTH comparison paths:
#
#   newer   -> refuses (non-zero, and says so)
#   older   -> proceeds
#
# A guard only ever observed passing is not evidence of a guard, so every
# refusal case here is asserted on its exit status and not merely on its output.
# The two paths are checked separately, because on the host that motivated this
# they are different binaries: $INSTALL_DIR/mg is the overwrite target, and
# `command -v mg` is what consumers actually resolve. Whichever one is guarded
# alone, the other is the one that bites.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SCRIPT="${SCRIPT_DIR}/install.sh"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

INSTALL_DIR="${tmpdir}/bin"
SHADOW_DIR="${tmpdir}/shadow"
PATHDIR="${tmpdir}/pathbin"
mkdir -p "$INSTALL_DIR" "$SHADOW_DIR" "$PATHDIR"

export INSTALL_DIR SHADOW_DIR
export MG_INSTALL_SKIP_MAIN=1

# A hermetic PATH, not a prepend. These tests assert on what `command -v mg`
# finds, so any real mg still reachable — ~/go/bin/mg, or the shadow symlink
# this repo's own installer drops at /usr/local/bin/mg — turns a fixture with
# "no resolved mg" into one that silently found the developer's own binary.
# (It does exactly that: prepending resolved ~/go/bin/mg and fired the guard for
# real, which is reassuring about the guard and useless as a test.) /usr/bin and
# /bin stay on it for the utilities install.sh itself calls.
PATH="${PATHDIR}:/usr/bin:/bin"
export PATH

# shellcheck disable=SC1090
. "$INSTALL_SCRIPT"

fail() { echo "FAIL: $*" >&2; exit 1; }

# A stand-in mg that reports $2, formatted exactly as `mg version` prints it.
mkmg() {
    cat > "$1" <<EOF
#!/bin/sh
[ "\$1" = version ] || exit 1
echo "mg $2 (abc1234, 2026-08-06)"
EOF
    chmod +x "$1"
}

rm_all() { rm -f "${INSTALL_DIR}/mg" "${PATHDIR}/mg"; }

LATEST=v0.3.0

echo "=== Test: version_fields parses releases, source builds and rejects junk ==="
[ "$(version_fields v0.3.0)" = "0 3 0 " ] || fail "v0.3.0: got '$(version_fields v0.3.0)'"
[ "$(version_fields 0.3.0)" = "0 3 0 " ] || fail "0.3.0 (goreleaser strips the v): got '$(version_fields 0.3.0)'"
got=$(version_fields "v0.3.1-dev.11+g0bbe682")
[ "$got" = "0 3 1 dev.11" ] || fail "source build: got '$got'"
for bad in dev "" "v1.2" "1.2.3.4" "abc" "1.x.3" "v-1.2.3"; do
    if version_fields "$bad" >/dev/null 2>&1; then
        fail "version_fields should reject '$bad'"
    fi
done
echo "  PASS"

echo "=== Test: version_is_newer orders the two producers correctly ==="
# 0 = newer, 1 = not newer, 2 = undecidable.
check_cmp() {
    _c=0
    version_is_newer "$1" "$2" || _c=$?
    [ "$_c" = "$3" ] || fail "version_is_newer '$1' '$2': expected $3, got $_c"
}
check_cmp v0.3.1 v0.3.0 0
check_cmp v0.3.0 v0.3.1 1
check_cmp v0.3.0 v0.3.0 1              # same version is not newer
check_cmp 0.3.0 v0.3.0 1               # release output vs tag: the v is stripped
check_cmp v1.0.0 v0.9.9 0
check_cmp v0.10.0 v0.9.0 0             # numeric fields, not lexicographic
check_cmp v0.9.0 v0.10.0 1
# What build.sh (mg-24dc) actually stamps: ahead of v0.3.0, behind v0.3.1.
check_cmp "v0.3.1-dev.11+g0bbe682" v0.3.0 0
check_cmp "v0.3.1-dev.11+g0bbe682" v0.3.1 1
check_cmp dev v0.3.0 2                 # the unstamped sentinel: undecidable
check_cmp v0.3.0 dev 2
echo "  PASS"

echo "=== Test: build metadata is excluded from precedence ==="
check_cmp "v0.3.0+gdeadbee" v0.3.0 1
check_cmp "v0.3.1+gdeadbee" v0.3.0 0
echo "  PASS"

echo "=== POSITIVE CONTROL: a NEWER binary at \$INSTALL_DIR/mg makes it refuse ==="
rm_all
mkmg "${INSTALL_DIR}/mg" v0.4.0
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "guard did not fire on a newer \$INSTALL_DIR/mg: $(cat "${tmpdir}/err")"
fi
grep -q "refusing to install" "${tmpdir}/err" || fail "refusal text missing: $(cat "${tmpdir}/err")"
grep -q "${INSTALL_DIR}/mg" "${tmpdir}/err" || fail "refusal must name the path it found"
grep -q "v0.4.0" "${tmpdir}/err" || fail "refusal must name the installed version"
grep -q -- "--force" "${tmpdir}/err" || fail "refusal must name the override flag"
grep -q "MG_FORCE=1" "${tmpdir}/err" || fail "refusal must name the override env var"
echo "  PASS"

echo "=== POSITIVE CONTROL: an OLDER binary at \$INSTALL_DIR/mg lets it proceed ==="
rm_all
mkmg "${INSTALL_DIR}/mg" v0.2.9
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "guard fired on an OLDER binary: $(cat "${tmpdir}/err")"
[ ! -s "${tmpdir}/err" ] || fail "older binary should install silently, got: $(cat "${tmpdir}/err")"
echo "  PASS"

echo "=== Test: the same version already installed proceeds (reinstall) ==="
rm_all
mkmg "${INSTALL_DIR}/mg" 0.3.0
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "guard fired on an identical version: $(cat "${tmpdir}/err")"
echo "  PASS"

echo "=== Test: no existing binary at all proceeds silently ==="
rm_all
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "guard fired with no binary present"
[ ! -s "${tmpdir}/err" ] || fail "clean install should be silent, got: $(cat "${tmpdir}/err")"
echo "  PASS"

echo "=== POSITIVE CONTROL: the OTHER path — newer \`command -v mg\` alone refuses ==="
# $INSTALL_DIR/mg absent, exactly as on the host that motivated this: the
# installer would not have overwritten the newer build, it would have written a
# second older one at a lower-precedence path.
rm_all
mkmg "${PATHDIR}/mg" v0.4.0
[ ! -e "${INSTALL_DIR}/mg" ] || fail "fixture wrong: \$INSTALL_DIR/mg should be absent"
[ "$(command -v mg)" = "${PATHDIR}/mg" ] || fail "fixture wrong: mg resolves to $(command -v mg)"
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "guard did not fire on a newer resolved mg: $(cat "${tmpdir}/err")"
fi
grep -q "${PATHDIR}/mg" "${tmpdir}/err" || fail "refusal must name the resolved path"
grep -q "resolved by PATH" "${tmpdir}/err" || fail "refusal must say WHICH path triggered it"
echo "  PASS"

echo "=== Test: an older resolved mg with no install target proceeds ==="
rm_all
mkmg "${PATHDIR}/mg" v0.2.0
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "guard fired on an older resolved mg: $(cat "${tmpdir}/err")"
echo "  PASS"

echo "=== Test: both paths are reported when both are newer and they differ ==="
rm_all
mkmg "${INSTALL_DIR}/mg" v0.4.0
mkmg "${PATHDIR}/mg" v0.5.0
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "guard did not fire when both paths are newer"
fi
grep -q "${INSTALL_DIR}/mg" "${tmpdir}/err" || fail "should name the install target"
grep -q "${PATHDIR}/mg" "${tmpdir}/err" || fail "should name the resolved path"
grep -q "v0.4.0" "${tmpdir}/err" || fail "should name the install target's version"
grep -q "v0.5.0" "${tmpdir}/err" || fail "should name the resolved binary's version"
echo "  PASS"

echo "=== Test: a newer resolved mg still refuses when the target is OLDER ==="
# The half a single-path guard misses: overwriting an old ~/.local/bin/mg is
# harmless in itself, but the shadow symlink then repoints at it and consumers
# lose the newer binary they were resolving.
rm_all
mkmg "${INSTALL_DIR}/mg" v0.1.0
mkmg "${PATHDIR}/mg" v0.9.0
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "guard must fire on the resolved path even when the target is older"
fi
grep -q "v0.9.0" "${tmpdir}/err" || fail "should name the newer resolved version"
echo "  PASS"

echo "=== THE OVERRIDE: --force / MG_FORCE=1 installs over a newer mg anyway ==="
rm_all
mkmg "${INSTALL_DIR}/mg" v0.4.0
saved_force="$FORCE"
FORCE=1
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "FORCE=1 must not refuse: $(cat "${tmpdir}/err")"
grep -q "because --force was given" "${tmpdir}/err" || fail "forced downgrade must still be announced: $(cat "${tmpdir}/err")"
grep -q "v0.4.0" "${tmpdir}/err" || fail "forced downgrade must name what it is overwriting"
FORCE="$saved_force"
echo "  PASS"

# --- the UNPARSEABLE cases ------------------------------------------------
#
# 'dev' is what an unstamped build reports, and build.sh falls back to it BY
# DESIGN (no git, no tags, a source tarball) — so this path is permanent, not a
# state to be waited out. It is the ABSENCE of a determination rather than a
# determination that this install is a downgrade, so it does not get the hard
# refusal that `newer` gets. It does not get a free pass either: whether warning
# is enough depends entirely on whether anyone is there to read it.
#
# stdin_is_tty is stubbed rather than a pty being allocated. Both stubs are
# restored immediately, because leaving one in place would quietly rewrite every
# test after it.

echo "=== Test: UNPARSEABLE down a PIPE refuses — a warning nobody reads is no refusal ==="
rm_all
mkmg "${INSTALL_DIR}/mg" dev
stdin_is_tty() { return 1; }        # a pipe: `curl ... | sh`
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "unparseable version must refuse non-interactively: $(cat "${tmpdir}/err")"
fi
grep -q "cannot be compared" "${tmpdir}/err" || fail "must say what is wrong: $(cat "${tmpdir}/err")"
grep -q "${INSTALL_DIR}/mg" "${tmpdir}/err" || fail "must name the path"
grep -q "'dev'" "${tmpdir}/err" || fail "must name what it read"
grep -q "non-interactively" "${tmpdir}/err" || fail "must say WHY it refused rather than asked"
grep -q -- "--force" "${tmpdir}/err" || fail "must name the flag that proceeds"
grep -q "MG_FORCE=1" "${tmpdir}/err" || fail "must name the env var that proceeds"
echo "  PASS"

echo "=== Test: UNPARSEABLE on a TERMINAL asks, and 'y' proceeds ==="
rm_all
mkmg "${INSTALL_DIR}/mg" dev
stdin_is_tty() { return 0; }        # a human is watching
check_existing_mg "$LATEST" 2>"${tmpdir}/err" <<'ANSWER' || fail "answering yes must proceed: $(cat "${tmpdir}/err")"
y
ANSWER
grep -q "anyway?" "${tmpdir}/err" || fail "must actually prompt: $(cat "${tmpdir}/err")"
echo "  PASS"

echo "=== Test: UNPARSEABLE on a TERMINAL asks, and anything else ABORTS ==="
for answer in n N "" junk; do
    rm_all
    mkmg "${INSTALL_DIR}/mg" dev
    if printf '%s\n' "$answer" | check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
        fail "answer '${answer}' should have aborted: $(cat "${tmpdir}/err")"
    fi
    grep -q "Aborted" "${tmpdir}/err" || fail "answer '${answer}': no abort message"
done
# The default is NO: a bare Enter must not install. [y/N] has to mean what it says.
echo "  PASS"

echo "=== Test: --force gets past the UNPARSEABLE refusal too ==="
rm_all
mkmg "${INSTALL_DIR}/mg" dev
stdin_is_tty() { return 1; }        # the pipe case, which would otherwise refuse
saved_force="$FORCE"
FORCE=1
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "--force must get past it: $(cat "${tmpdir}/err")"
grep -q "Installing anyway because --force" "${tmpdir}/err" || fail "must say it proceeded on --force"
FORCE="$saved_force"
echo "  PASS"

echo "=== Test: a binary that will not answer \`version\` is treated as unparseable ==="
rm_all
cat > "${INSTALL_DIR}/mg" <<'EOF'
#!/bin/sh
echo "this is not macguffin"
EOF
chmod +x "${INSTALL_DIR}/mg"
stdin_is_tty() { return 1; }
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "an unreadable binary must refuse non-interactively"
fi
grep -q "cannot be compared" "${tmpdir}/err" || fail "should say the version could not be compared"
echo "  PASS"

echo "=== Test: NOTHING installed still proceeds silently down a pipe ==="
# The narrowness that makes the above liveable: a fresh CI install has no binary
# to be ambiguous about, so it is untouched by any of this.
rm_all
stdin_is_tty() { return 1; }
check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "a fresh install must not be blocked"
[ ! -s "${tmpdir}/err" ] || fail "a fresh install must be silent, got: $(cat "${tmpdir}/err")"
echo "  PASS"

# Back to the real thing for everything below.
stdin_is_tty() { [ -t 0 ]; }

echo "=== Test: a NEWER binary is refused even when the other path is unparseable ==="
# A refusal outranks a warning; the operator has to be told about the downgrade.
rm_all
mkmg "${INSTALL_DIR}/mg" v0.4.0
mkmg "${PATHDIR}/mg" dev
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "a newer binary must still refuse when the other path is unparseable"
fi
grep -q "refusing to install" "${tmpdir}/err" || fail "refusal text missing"
echo "  PASS"

echo "=== Test: /usr/bin/mg (microemacs) is not treated as a previous install ==="
# warn_system_mg and shadow_link already own that path. Running it to ask its
# version would be asking an editor for a version string.
rm_all
if [ -e /usr/bin/mg ]; then
    saved_path="$PATH"
    PATH="/usr/bin:${PATHDIR}"
    export PATH
    [ "$(command -v mg)" = "/usr/bin/mg" ] || fail "fixture wrong: mg resolves to $(command -v mg)"
    check_existing_mg "$LATEST" 2>"${tmpdir}/err" || fail "system mg must not make this refuse"
    [ ! -s "${tmpdir}/err" ] || fail "system mg should produce no guard output, got: $(cat "${tmpdir}/err")"
    PATH="$saved_path"
    export PATH
    echo "  PASS"
else
    echo "  SKIP: /usr/bin/mg not present on this system"
fi

echo "=== Test: the shadow symlink is not reported as a second binary ==="
rm_all
mkmg "${INSTALL_DIR}/mg" v0.4.0
ln -sf "${INSTALL_DIR}/mg" "${PATHDIR}/mg"
if check_existing_mg "$LATEST" 2>"${tmpdir}/err"; then
    fail "guard should still fire on a newer binary reached via a symlink"
fi
count=$(grep -c "reports v0.4.0" "${tmpdir}/err")
[ "$count" -eq 1 ] || fail "the same binary was reported $count times, expected 1"
rm_all

echo "  PASS"

echo ""
echo "=== All install-guard tests passed ==="
