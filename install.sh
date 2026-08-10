#!/bin/sh
# Install macguffin binary from the latest GitHub release.
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/drellem2/macguffin/main/install.sh | sh
#   sh install.sh              # install to ~/.local/bin
#   INSTALL_DIR=/usr/local/bin sh install.sh
#   sh install.sh --force      # install even over a newer mg (downgrade)
#   curl -sSfL <url> | sh -s -- --force
#
# Environment:
#   INSTALL_DIR   Where to put the mg binary (default: ~/.local/bin).
#   SHADOW_DIR    Directory for the shadow symlink that hides /usr/bin/mg
#                 (microemacs) on macOS/Linux. Default: /usr/local/bin.
#   SHADOW_MG     Set to 0 to skip the shadow symlink. Default: 1.
#   MG_FORCE      Set to 1 to install over a newer mg. Same as --force.
#   GITHUB_TOKEN  Optional. A token to authenticate the one GitHub API call this
#   GH_TOKEN      script makes (looking up the latest release tag). Unset is the
#                 normal case and works fine; see github_api() for when it isn't.
#
# Supports: Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD (amd64)

set -e

REPO="drellem2/macguffin"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
SHADOW_DIR="${SHADOW_DIR:-/usr/local/bin}"
SHADOW_MG="${SHADOW_MG:-1}"

case "${MG_FORCE:-}" in
    ''|0) FORCE=0 ;;
    *)    FORCE=1 ;;
esac

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

# GET a GitHub REST API URL, authenticated only if the caller supplied a token.
#
# The API rate-limits unauthenticated callers by SOURCE IP, 60 requests/hour.
# For a person running `curl … | sh` on their own machine that is ample and no
# credential is wanted — so the default path here sends none, and must keep
# sending none. CI is the exception: GitHub Actions runners share a NAT pool
# with every other job on the platform, so that per-IP quota is routinely
# already spent by strangers and the call comes back 403 with nothing wrong on
# this end. An authenticated call is metered against the token instead (5000/hr),
# which is why the workflow passes GITHUB_TOKEN into the install job rather than
# this script carrying a credential of its own.
#
# The token goes to curl through a --config file on stdin, not through
# `-H "Authorization: …"`, because argv is world-readable via ps(1) and this
# script also runs on multi-user machines. Nothing here echoes it.
#
# The quotes around the header value are load-bearing. curl's config parser
# takes an UNQUOTED value only up to the first space, so `header = Authorization:
# Bearer xyz` sets the header to a bare "Authorization:" — which curl reads as
# "suppress this header" and sends no credential at all, with no error. That
# failure is invisible and looks exactly like the bug being fixed; it is asserted
# against the real curl in scripts/test-install-auth.sh.
github_api() {
    _api_token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
    if [ -n "$_api_token" ]; then
        printf 'header = "Authorization: Bearer %s"\n' "$_api_token" \
            | curl -sSfL --config - "$1"
    else
        curl -sSfL "$1"
    fi
}

get_latest_version() {
    github_api "https://api.github.com/repos/${REPO}/releases/latest" \
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

# One level of symlink resolution. Enough to recognise this script's own shadow
# symlink as the same binary it points at, without depending on `readlink -f`,
# which is absent on older macOS and not in POSIX.
resolve_link() {
    if [ -L "$1" ]; then
        _t="$(readlink "$1")"
        case "$_t" in
            /*) echo "$_t" ;;
            *)  echo "$(dirname "$1")/$_t" ;;
        esac
    else
        echo "$1"
    fi
}

# --- version comparison --------------------------------------------------
#
# Only two things stamp a version into an mg binary. goreleaser stamps the
# release tag WITHOUT its leading `v` (.goreleaser.yml uses {{.Version}}), so a
# release prints `mg 0.3.0 (f82a2e8, ...)` while get_latest_version() returns
# the tag `v0.3.0` — the `v` has to be stripped on both sides. build.sh stamps a
# source build as v<M>.<m>.<p+1>-dev.<distance>+g<sha> (mg-24dc). Releases are
# always plain vN.N.N.
#
# So the ordering needed here is semver CORE ordering plus exactly one semver
# precedence rule — a pre-release sorts BEFORE the release with the same core —
# and nothing more. `sort -V` would do it on GNU coreutils and on macOS 15, but
# install.sh declares FreeBSD support too, so the fields are compared as numbers
# by hand rather than betting on a `sort` flag across three platforms.

# Split a version into "MAJOR MINOR PATCH [PRERELEASE]". Strips a leading `v`
# and any `+build` metadata, which semver excludes from precedence. Returns
# non-zero — printing nothing — when the string is not N.N.N[-pre][+build].
# That includes the `dev` sentinel an unstamped build reports.
version_fields() {
    _v="${1#v}"
    _v="${_v%%+*}"
    _pre=""
    case "$_v" in
        *-*) _pre="${_v#*-}"; _v="${_v%%-*}" ;;
    esac
    case "$_v" in
        *.*.*.*) return 1 ;;
        *.*.*)   ;;
        *)       return 1 ;;
    esac
    _maj="${_v%%.*}"
    _rest="${_v#*.}"
    _min="${_rest%%.*}"
    _pat="${_rest##*.}"
    for _f in "$_maj" "$_min" "$_pat"; do
        case "$_f" in
            ''|*[!0-9]*) return 1 ;;
        esac
    done
    echo "$_maj $_min $_pat $_pre"
}

# Is $1 strictly newer than $2?
#   0  yes
#   1  no (older, or the same)
#   2  undecidable — one of them is not a version at all
version_is_newer() {
    _fa="$(version_fields "$1")" || return 2
    _fb="$(version_fields "$2")" || return 2

    # shellcheck disable=SC2086
    set -- $_fa
    _amaj=$1 _amin=$2 _apat=$3 _apre=${4:-}
    # shellcheck disable=SC2086
    set -- $_fb
    _bmaj=$1 _bmin=$2 _bpat=$3 _bpre=${4:-}

    if [ "$_amaj" -ne "$_bmaj" ]; then
        if [ "$_amaj" -gt "$_bmaj" ]; then return 0; else return 1; fi
    fi
    if [ "$_amin" -ne "$_bmin" ]; then
        if [ "$_amin" -gt "$_bmin" ]; then return 0; else return 1; fi
    fi
    if [ "$_apat" -ne "$_bpat" ]; then
        if [ "$_apat" -gt "$_bpat" ]; then return 0; else return 1; fi
    fi

    # Cores are equal. A pre-release sorts BEFORE the release with the same core
    # (semver §11.3), so v0.3.1-dev.11 is correctly OLDER than 0.3.1 and this
    # script may overwrite it.
    if [ -n "$_apre" ] && [ -z "$_bpre" ]; then return 1; fi
    if [ -z "$_apre" ] && [ -n "$_bpre" ]; then return 0; fi
    if [ "$_apre" != "$_bpre" ]; then
        # Two DIFFERENT pre-releases on the same core. Unreachable while the
        # right-hand side is a published release tag, which is always plain
        # vN.N.N — so rather than hand-rolling identifier-by-identifier
        # precedence for a case that cannot occur, say so and let the caller
        # warn instead of guessing.
        return 2
    fi
    return 1
}

# The version an mg binary reports, verbatim (leading `v` and all). Prints
# nothing and returns non-zero when there is no runnable binary there, or when
# what runs does not answer like mg does.
#
# KNOWN AND ACCEPTED: this exec is UNBOUNDED. If the binary it asks hangs —
# wedged, or merely very slow on a box under heavy load — install.sh hangs with
# it, possibly inside a `curl | sh`. There is no portable `timeout` (macOS ships
# none), and bounding it by hand would mean backgrounding and polling a
# subprocess in a script whose whole job is to be simple, so it is left
# unbounded deliberately rather than by oversight. The /usr/bin/mg skip and the
# </dev/null below remove the one case anybody has actually hit.
installed_version() {
    _bin="$1"
    [ -n "$_bin" ] && [ -x "$_bin" ] || return 1
    # stdin from /dev/null: whatever is at that path is not necessarily mg, and
    # this can run inside `curl | sh` on a tty. An editor that inherited the
    # terminal would sit there waiting for input.
    _out="$("$_bin" version 2>/dev/null </dev/null)" || return 1
    case "$_out" in
        "mg "*) ;;
        *) return 1 ;;
    esac
    _out="${_out#mg }"
    _out="${_out%% *}"
    [ -n "$_out" ] || return 1
    printf '%s\n' "$_out"
}

# What an existing binary at $1 means for an install of version $2. Echoes the
# state, then the version it reported when there was one:
#
#   absent            nothing there
#   older <version>   older, or the same — this install may proceed
#   newer <version>   this install would be a DOWNGRADE
#   unknown [version] present, but nothing orderable came back
existing_state() {
    _p="$1"
    _latest="$2"

    if [ -z "$_p" ] || [ ! -e "$_p" ]; then
        echo absent
        return 0
    fi

    if ! _ver="$(installed_version "$_p")"; then
        # Something is at that path but it will not say what it is. Not
        # orderable either, so it lands in the same bucket as `dev`.
        echo unknown
        return 0
    fi

    _cmp=0
    version_is_newer "$_ver" "$_latest" || _cmp=$?
    case "$_cmp" in
        0) echo "newer $_ver" ;;
        1) echo "older $_ver" ;;
        *) echo "unknown $_ver" ;;
    esac
}

# --- refusing to overwrite a newer mg ------------------------------------
#
# install.sh only ever installs RELEASES, so the hazard is one-directional:
# running it on a host that already has a NEWER mg silently downgrades that
# host, and the loss surfaces much later as a command that used to exist and now
# does not. Across the 201 lines this script had before, no existing mg's
# version was ever read.
#
# TWO paths are checked, not one:
#
#   $INSTALL_DIR/mg   the file this script is about to overwrite
#   command -v mg     what actually resolves for consumers
#
# On the host this was reported from they are DIFFERENT BINARIES — the default
# target ~/.local/bin/mg did not exist at all while `mg` resolved to
# ~/go/bin/mg — so the install would not have overwritten the newer build. It
# would have written a second, older binary at a lower-precedence path and
# repointed the shadow symlink at it, after which which mg a given consumer gets
# is PATH-dependent. Guarding either path alone leaves the other open, and which
# one bites is not predictable from here.
#
# NEWER and UNPARSEABLE are deliberately NOT treated alike:
#
#   newer        A positive determination that this install is a downgrade.
#                Refused, because README documents `curl -sSfL ... | sh` as the
#                entry point, and in a pipe a warning scrolls past unread.
#                --force / MG_FORCE=1 is there for the deliberate downgrade.
#
#   unparseable  The ABSENCE of a determination, not a determination of danger.
#                Warned, not refused.
#
#                That distinction is the whole design, so the reasons it survives
#                review are worth keeping. build.sh derives a version only when it
#                has a git checkout with a vN.N.N tag to derive it from, and falls
#                back to the unstamped `dev` sentinel BY DESIGN otherwise — a
#                source tarball, a checkout with no tags (build.sh:40-46). So the
#                unparseable path is PERMANENT, and since mg-24dc landed its
#                population is no longer dev boxes running newer code; it is that
#                documented residue. Refusing on it would block installs on the
#                strength of no evidence, at a false-positive rate that never
#                reaches zero, and the cost would fall on automation that
#                legitimately wants the release — a CI container with no git,
#                reinstalling over a tarball-built mg.
#
#                Note also which case this predicate does NOT cover, because it is
#                the one people picture when they argue for refusing here: a FRESH
#                `curl | sh` never reaches it at all. With no existing mg there is
#                nothing to compare, so the check does not fire. "The piped case is
#                primary" is true of install.sh generally and false of this
#                predicate specifically.
#
#                And the harms are asymmetric in the same direction. Proceeding
#                wrongly is recoverable — the source is still in git or the
#                tarball, so a reinstall undoes it. A pipeline that cannot install
#                mg at all is simply blocked.
check_existing_mg() {
    latest="$1"

    target="$(abspath "$INSTALL_DIR")/mg"
    resolved=""
    if _r="$(command -v mg 2>/dev/null)"; then
        resolved="$(resolve_link "$(abspath "$_r")")"
    fi

    # /usr/bin/mg is microemacs on macOS and on some Linux distros; warn_system_mg
    # covers it and shadow_link exists to hide it. It is not a previous install of
    # this tool, so it is not this guard's business.
    if [ "$resolved" = "/usr/bin/mg" ] || [ "$resolved" = "$target" ]; then
        resolved=""
    fi

    newer_report=""
    unknown_report=""

    for _entry in "target:$target" "resolved:$resolved"; do
        _kind="${_entry%%:*}"
        _path="${_entry#*:}"
        [ -n "$_path" ] || continue

        _label="$_path"
        if [ "$_kind" = resolved ]; then
            _label="$_path (resolved by PATH as 'mg')"
        fi

        _state="$(existing_state "$_path" "$latest")"
        # A bare state word carries no version; "${_state#* }" is a no-op then.
        _ver="${_state#* }"
        if [ "$_ver" = "$_state" ]; then
            _ver=""
        fi
        case "${_state%% *}" in
            newer)
                newer_report="${newer_report}  ${_label}
      reports ${_ver}, which is NEWER than ${latest}
"
                ;;
            unknown)
                if [ -n "$_ver" ]; then
                    _why="reports '${_ver}', which cannot be ordered against ${latest}"
                else
                    _why="did not report a version at all"
                fi
                unknown_report="${unknown_report}  ${_label}
      ${_why}
"
                ;;
        esac
    done

    if [ -n "$newer_report" ]; then
        if [ "$FORCE" = "1" ]; then
            echo "" >&2
            echo "Warning: installing ${latest} over a NEWER mg because --force was given:" >&2
            printf '%s' "$newer_report" >&2
            echo "" >&2
        else
            echo "" >&2
            echo "Error: refusing to install mg ${latest} over a newer mg." >&2
            echo "" >&2
            printf '%s' "$newer_report" >&2
            echo "" >&2
            echo "Installing would replace it with the older release ${latest}, and the" >&2
            echo "commands it has and ${latest} does not would just stop existing." >&2
            echo "" >&2
            echo "To downgrade deliberately, re-run with --force:" >&2
            echo "" >&2
            echo "    sh install.sh --force" >&2
            echo "    curl -sSfL https://raw.githubusercontent.com/${REPO}/main/install.sh | sh -s -- --force" >&2
            echo "" >&2
            echo "or set MG_FORCE=1 in the environment." >&2
            echo "" >&2
            return 1
        fi
    fi

    if [ -n "$unknown_report" ]; then
        echo "" >&2
        echo "Warning: an existing mg is present whose version cannot be compared:" >&2
        printf '%s' "$unknown_report" >&2
        echo "" >&2
        echo "An unstamped source build reports 'dev', which is not orderable against a" >&2
        echo "release, so this installer cannot tell whether ${latest} is an upgrade or a" >&2
        echo "downgrade for it. Installing anyway. Build from a tagged checkout with" >&2
        echo "./build.sh to get a version that can be compared." >&2
        echo "" >&2
    fi

    return 0
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
    for arg in "$@"; do
        case "$arg" in
            -f|--force) FORCE=1 ;;
            *)
                echo "Unknown option: $arg" >&2
                echo "Usage: sh install.sh [--force]" >&2
                exit 1
                ;;
        esac
    done

    os="$(detect_os)"
    arch="$(detect_arch)"
    platform="${os}_${arch}"
    version="$(get_latest_version)"

    if [ -z "$version" ]; then
        echo "Error: could not determine latest version" >&2
        # The overwhelmingly likely cause of a 403 on that call, and the one
        # thing the caller can do about it. Only worth saying when the call was
        # in fact unauthenticated; with a token set, this is the wrong advice.
        if [ -z "${GITHUB_TOKEN:-${GH_TOKEN:-}}" ]; then
            echo "  If that was an HTTP 403, the GitHub API is rate-limiting this IP." >&2
            echo "  Retry later, or set GITHUB_TOKEN to raise the limit." >&2
        fi
        exit 1
    fi

    # Before the download, not after: there is nothing to gain from fetching a
    # release this host is going to refuse to install.
    if ! check_existing_mg "$version"; then
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
