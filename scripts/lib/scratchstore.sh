# shellcheck shell=sh
# A throwaway mg store for the shell suites, and the refusal that keeps them
# off the real one.
#
# WHY THIS EXISTS. scripts/e2e_milestones_test.sh and scripts/event_test.sh
# used to reset their state with a clean() that recursively deleted the default
# store under the invoking user's home. On 2026-09-27 a polecat ran one of them
# by hand and it deleted every work item and every mailbox on the machine, with
# no backup (mg-9a40). A suite that needs a store now gets one from here: a
# scratch $HOME and a scratch $MG_ROOT inside the swept test root, and nothing
# that resets state ever deletes — clean() moves MG_ROOT to a FRESH directory
# instead, and the whole scratch tree goes from the caller's trap.
#
# USAGE
#
#     . "${REPO_ROOT}/scripts/lib/testtmp.sh"
#     . "${REPO_ROOT}/scripts/lib/scratchstore.sh"
#     scratchstore_setup event-test || exit 1
#     trap 'testtmp_remove "$SCRATCHSTORE_DIR"' EXIT INT TERM HUP
#     clean() { scratchstore_fresh || exit 1; }
#
# scratchstore_setup exports HOME and MG_ROOT and refuses (non-zero, message on
# stderr) if either would land on the real user's home or its store. Call
# scratchstore_assert_safe again before anything that writes; scratchstore_fresh
# already does.

# scratchstore_real_home prints the invoking user's home as the password
# database has it — NOT $HOME, which is exactly the variable a scratch store
# overrides, and which a caller may already have pointed somewhere else. The
# tilde-user expansion reads the passwd entry on both darwin and linux.
scratchstore_real_home() {
    _ss_user=$(id -un 2>/dev/null) || return 1
    case "$_ss_user" in
        '' | *[!A-Za-z0-9._-]*) return 1 ;;
    esac
    eval "_ss_home=~$_ss_user"
    case "$_ss_home" in
        '~'*) return 1 ;; # no passwd entry: the expansion did not happen
    esac
    printf '%s\n' "$_ss_home"
}

# scratchstore_phys prints $1 with symlinks resolved when it exists, else $1
# unchanged. /tmp -> /private/tmp on darwin, so a string compare alone would
# let a symlinked spelling of the real home through.
scratchstore_phys() {
    if [ -d "$1" ]; then
        (cd "$1" 2>/dev/null && pwd -P) || printf '%s\n' "$1"
    else
        printf '%s\n' "$1"
    fi
}

# scratchstore_is_live reports whether $1 is the real home, or the real store
# (<real home>/.macguffin) or anything inside it.
scratchstore_is_live() {
    _ss_p=$(scratchstore_phys "$1")
    for _ss_h in "$SCRATCHSTORE_REAL_HOME" "$SCRATCHSTORE_ENTRY_HOME"; do
        [ -n "$_ss_h" ] || continue
        _ss_h=$(scratchstore_phys "$_ss_h")
        case "$_ss_p" in
            "$_ss_h" | "$_ss_h/" | "$_ss_h/.macguffin" | "$_ss_h/.macguffin/"*) return 0 ;;
        esac
    done
    return 1
}

# scratchstore_assert_safe refuses unless HOME and MG_ROOT are both set, both
# inside $SCRATCHSTORE_DIR, and neither is the live home or store.
scratchstore_assert_safe() {
    if [ -z "$SCRATCHSTORE_DIR" ] || [ -z "$HOME" ] || [ -z "$MG_ROOT" ]; then
        echo "scratchstore: REFUSING: HOME, MG_ROOT and the scratch dir must all be set" >&2
        return 1
    fi
    for _ss_v in "$HOME" "$MG_ROOT"; do
        if scratchstore_is_live "$_ss_v"; then
            echo "scratchstore: REFUSING: $_ss_v is the real user home or its store" >&2
            return 1
        fi
        case "$_ss_v" in
            "$SCRATCHSTORE_DIR"/*) ;;
            *)
                echo "scratchstore: REFUSING: $_ss_v is outside the scratch dir $SCRATCHSTORE_DIR" >&2
                return 1
                ;;
        esac
    done
    return 0
}

# scratchstore_fresh points MG_ROOT at a new, empty directory. It never deletes:
# the old store stays where it was until the trap removes the scratch tree.
scratchstore_fresh() {
    MG_ROOT=$(mktemp -d "$SCRATCHSTORE_DIR/store.XXXXXX") || {
        echo "scratchstore: cannot create a store under $SCRATCHSTORE_DIR" >&2
        return 1
    }
    export MG_ROOT
    scratchstore_assert_safe
}

# scratchstore_setup <purpose> creates the scratch tree, exports HOME and
# MG_ROOT into it, and sets SCRATCHSTORE_DIR for the caller's trap.
scratchstore_setup() {
    # Both homes are recorded BEFORE HOME is overridden: the passwd one because
    # it cannot be spoofed by the environment, the entry one because it is the
    # home this shell was actually going to write to.
    SCRATCHSTORE_REAL_HOME=$(scratchstore_real_home) || SCRATCHSTORE_REAL_HOME=""
    SCRATCHSTORE_ENTRY_HOME=${HOME:-}
    if [ -z "$SCRATCHSTORE_REAL_HOME" ] && [ -z "$SCRATCHSTORE_ENTRY_HOME" ]; then
        echo "scratchstore: REFUSING: cannot determine the real home to protect" >&2
        return 1
    fi
    SCRATCHSTORE_DIR=$(testtmp_dir "$1") || return 1
    SCRATCHSTORE_DIR=$(scratchstore_phys "$SCRATCHSTORE_DIR")
    mkdir -p "$SCRATCHSTORE_DIR/home" || return 1
    HOME="$SCRATCHSTORE_DIR/home"
    export HOME
    scratchstore_fresh
}
