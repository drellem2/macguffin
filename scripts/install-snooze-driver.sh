#!/bin/sh
# Register the periodic run of `mg schedule`.
#
# THIS IS NO LONGER WHAT OPENS A SNOOZE. Every `mg` invocation promotes pending
# items whose wake time has passed, on a goroutine beside whatever you ran, so a
# gate opens on the next mg command of any kind. Readiness used to depend on this
# schedule existing, and when it was lost the next sweep reported "the previous
# sweep ran 4d 9h ago" — four days of gates that had opened and stayed shut.
#
# What this schedule is still for is the REPORTS, which nothing else produces:
#
#   - the held report: every pending item and the gates holding it;
#   - the stranded report: items no completion can ever release — waiting on a
#     shelved or nonexistent parent — which automatic promotion by construction
#     can never fix, and which are invisible from every other angle;
#   - the dependency-gate sweep, and the tidy-up of spent gates;
#   - the check that promotion is actually WORKING: a pending item with every
#     gate open right after a sweep means the store cannot be written.
#
# So this is still worth registering — it is just no longer load-bearing for an
# item's readiness, and `mg snooze` no longer refuses without it.
#
# pogod is the driver because it is the one that already survives host sleep,
# NTP steps and its own restarts: schedules persist to disk and replay on wake,
# which an in-process cron does not. The sweep is level-triggered, so a fire
# missed while pogod was down delays an item and can never lose one.
#
# Usage:
#   scripts/install-snooze-driver.sh                 # every 15 minutes, via mayor
#   AGENT=pa CRON="*/5 * * * *" scripts/install-snooze-driver.sh
#
# Re-running replaces the same (agent, id) entry rather than stacking duplicates.

set -eu

AGENT="${AGENT:-mayor}"
CRON="${CRON:-*/15 * * * *}"
ID="${ID:-mg-schedule-sweep}"

if ! command -v pogo >/dev/null 2>&1; then
    echo "error: pogo is not on PATH — this driver runs on pogod." >&2
    echo "Any periodic runner works; the sweep only needs to be run." >&2
    echo "The requirement is simply: run 'mg schedule' on a clock." >&2
    exit 1
fi

pogo schedule "$AGENT" \
    --cron "$CRON" \
    --id "$ID" \
    --replay once \
    --message "Run: mg schedule — promote pending items whose gates have opened (dependencies met, snooze times elapsed) and report anything it could not promote."

echo ""
echo "Registered '$ID' on $AGENT ($CRON)."
echo "Confirm with: pogo schedule list --agent $AGENT"
echo "Verify it is landing with: cat \"\${MG_ROOT:-\$HOME/.macguffin}\"/work/.last-sweep"
