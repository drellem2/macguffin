#!/bin/sh
# Test that install.sh's one GitHub API call is authenticated when — and only
# when — the caller supplies a token.
#
# Hermetic by construction: a recording stub `curl` on PATH and, for the last
# section, the real curl aimed at a dead local port. Nothing here talks to
# api.github.com. A test for this that made a real API call would be subject to
# the very rate limit it exists to guard, which is how CI got into this state:
# the install job's 403 had nothing to do with the script and everything to do
# with sharing an IP pool with the rest of GitHub Actions.
#
# Two properties, and the second is the one that bites:
#
#   1. WHETHER a credential is sent. Unauthenticated is correct — and must stay
#      correct — for a person running `curl … | sh`; only CI needs the token,
#      and it supplies it from the environment.
#
#   2. WHETHER curl actually sends the one it was given. install.sh passes the
#      header through a --config file, and curl's config parser truncates an
#      UNQUOTED value at the first space: `header = Authorization: Bearer xyz`
#      arrives as the valueless header "Authorization:", which curl reads as
#      "suppress this header" and sends no credential at all — no warning, no
#      error, exit 0. A stub curl cannot see that, because from the stub's side
#      the config text looks fine either way. So the last section runs the REAL
#      curl and reads back the header it would have put on the wire, with a
#      negative control proving the assertion can fail.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SCRIPT="${SCRIPT_DIR}/install.sh"

# The real curl, resolved BEFORE the stub goes on PATH.
REAL_CURL="$(command -v curl)"

# The swept root, not the shared $TMPDIR — see scripts/lib/testtmp.sh. The trap
# below is still what reclaims the space on the ordinary path; what the helper
# adds is the reclaim that does not depend on it, because a trap does not fire on
# SIGKILL and this one did not name TERM/INT/HUP either (mg-cc3f).
# shellcheck source=lib/testtmp.sh
. "${SCRIPT_DIR}/scripts/lib/testtmp.sh"
tmpdir="$(testtmp_dir test-install-auth)" || exit 1
trap 'testtmp_remove "$tmpdir"' EXIT INT TERM HUP

STUBDIR="${tmpdir}/stubbin"
mkdir -p "$STUBDIR"

fail() { echo "FAIL: $*" >&2; exit 1; }

# A stub curl that records how it was called and answers as the releases API
# would. It reads stdin only when --config is present, so the no-token case
# cannot hang on a terminal.
cat > "${STUBDIR}/curl" <<EOF
#!/bin/sh
_cfg=
for _a in "\$@"; do
    printf '%s\n' "\$_a" >> "${tmpdir}/argv"
    if [ "\$_a" = "--config" ]; then _cfg=1; fi
done
if [ -n "\$_cfg" ]; then cat > "${tmpdir}/stdin"; fi
echo '{ "tag_name": "v9.9.9", "name": "mg 9.9.9" }'
EOF
chmod +x "${STUBDIR}/curl"

# Hermetic, not a prepend: the point is that the real curl is unreachable.
PATH="${STUBDIR}:/usr/bin:/bin"
export PATH

export MG_INSTALL_SKIP_MAIN=1
export INSTALL_DIR="${tmpdir}/bin"
# shellcheck disable=SC1090
. "$INSTALL_SCRIPT"

# Run get_latest_version with a clean slate, and prove the stub actually ran.
# Without this last check every "no credential was sent" assertion below would
# pass just as happily against a curl that was never invoked.
call() {
    rm -f "${tmpdir}/argv" "${tmpdir}/stdin"
    got="$(get_latest_version)"
    [ -f "${tmpdir}/argv" ] || fail "$1: the curl stub never ran; the assertions would be vacuous"
    [ "$got" = "v9.9.9" ] || fail "$1: expected tag v9.9.9, got '${got}'"
}

sent_config() { grep -qx -- "--config" "${tmpdir}/argv"; }
argv_has() { grep -q -- "$1" "${tmpdir}/argv"; }

echo "=== POSITIVE CONTROL: no token in the environment sends NO credential ==="
# The end-user path, and the reason install.sh carries no credential of its own.
unset GITHUB_TOKEN GH_TOKEN
call "no token"
if sent_config; then fail "unauthenticated call used a --config file"; fi
if argv_has "Authorization"; then fail "unauthenticated call sent an Authorization header"; fi
[ ! -f "${tmpdir}/stdin" ] || fail "unauthenticated call wrote a config on stdin"
echo "  PASS: plain unauthenticated GET"

echo "=== GITHUB_TOKEN is sent, as an Authorization header ==="
GITHUB_TOKEN=tok-github-1234; export GITHUB_TOKEN
unset GH_TOKEN
call "GITHUB_TOKEN"
sent_config || fail "GITHUB_TOKEN set but no --config file was used"
grep -q 'header = "Authorization: Bearer tok-github-1234"' "${tmpdir}/stdin" \
    || fail "config does not carry the token: $(cat "${tmpdir}/stdin")"
echo "  PASS"

echo "=== POSITIVE CONTROL: the token never appears in argv ==="
# argv is world-readable through ps(1), and install.sh runs on multi-user boxes.
# This is why the header goes through a config file on stdin at all, so it is
# asserted rather than assumed.
if argv_has "tok-github-1234"; then
    fail "the token is visible in curl's argv: $(cat "${tmpdir}/argv")"
fi
echo "  PASS: nothing in argv exposes it"

echo "=== GH_TOKEN works too, and GITHUB_TOKEN wins when both are set ==="
unset GITHUB_TOKEN
GH_TOKEN=tok-gh-5678; export GH_TOKEN
call "GH_TOKEN"
grep -q 'Authorization: Bearer tok-gh-5678' "${tmpdir}/stdin" \
    || fail "GH_TOKEN was not used: $(cat "${tmpdir}/stdin")"

GITHUB_TOKEN=tok-github-1234; export GITHUB_TOKEN
call "both tokens"
grep -q 'Authorization: Bearer tok-github-1234' "${tmpdir}/stdin" \
    || fail "GITHUB_TOKEN should take precedence: $(cat "${tmpdir}/stdin")"
echo "  PASS"

echo "=== An EMPTY token is unset, not a credential ==="
# CI exports empty strings routinely — a fork PR, a `env:` key whose secret is
# absent. Sending `Authorization: Bearer ` would turn a working anonymous call
# into a 401, i.e. make the fix worse than the bug for anyone but CI.
GITHUB_TOKEN=""; export GITHUB_TOKEN
unset GH_TOKEN
call "empty GITHUB_TOKEN"
if sent_config; then fail "an empty GITHUB_TOKEN was treated as a credential"; fi
echo "  PASS: falls back to the unauthenticated call"

echo "=== An empty GITHUB_TOKEN falls through to GH_TOKEN ==="
GITHUB_TOKEN=""; export GITHUB_TOKEN
GH_TOKEN=tok-gh-5678; export GH_TOKEN
call "empty GITHUB_TOKEN, set GH_TOKEN"
grep -q 'Authorization: Bearer tok-gh-5678' "${tmpdir}/stdin" \
    || fail "GH_TOKEN should be used when GITHUB_TOKEN is empty"
echo "  PASS"

echo "=== THE WIRE: the real curl parses that config into a real header ==="
# Property 2. The stub above proves install.sh emits the right text; only curl
# can say whether curl agrees. --libcurl dumps the request curl assembled, and
# --connect-to sends it at a dead local port, so nothing leaves this machine.
unset GITHUB_TOKEN GH_TOKEN
trace_header() {
    rm -f "${tmpdir}/dump.c"
    printf '%s\n' "$1" | "$REAL_CURL" -sSfL --config - \
        --libcurl "${tmpdir}/dump.c" \
        --connect-to api.github.com:443:127.0.0.1:1 \
        --connect-timeout 2 \
        "https://api.github.com/repos/drellem2/macguffin/releases/latest" \
        >/dev/null 2>&1 || true
}

trace_header 'header = "Authorization: Bearer tok-wire-9999"'
if [ ! -f "${tmpdir}/dump.c" ]; then
    echo "  SKIP: this curl produced no --libcurl dump; wire check not run"
else
    grep -q 'curl_slist_append(slist1, "Authorization: Bearer tok-wire-9999")' "${tmpdir}/dump.c" \
        || fail "the real curl did not put the full credential on the request: $(grep curl_slist_append "${tmpdir}/dump.c" || echo '(no headers at all)')"
    echo "  PASS: quoted config -> Authorization: Bearer tok-wire-9999"

    # NEGATIVE CONTROL: the same line without quotes. If this also "passed", the
    # assertion above would be worthless — and this is not hypothetical, it is
    # exactly what curl does with an unquoted value.
    trace_header 'header = Authorization: Bearer tok-wire-9999'
    if grep -q 'Bearer tok-wire-9999' "${tmpdir}/dump.c"; then
        fail "negative control failed: unquoted config unexpectedly carried the token"
    fi
    echo "  PASS: unquoted config silently drops it — which is why install.sh quotes"
fi

echo ""
echo "=== All install-auth tests passed ==="
