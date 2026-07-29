#!/bin/sh
# Register the driver that opens snooze gates.
#
# `mg snooze` writes a `snooze:` attribute onto a pending item; `mg schedule` is
# what reads it and returns the item to available/. Without something running
# that sweep on a clock, a snoozed item is invisible — it is not available/, so
# stall-watch and priority-wake cannot see it, and "pending" is exactly what a
# correctly-waiting item looks like. That is how two items sat fifteen days
# behind a gate whose date had already passed.
#
# So `mg snooze` REFUSES to set a gate when nothing has driven the sweep
# recently (see internal/workitem/snooze.go, DriverStaleAfter). This script is
# the one-line answer to that refusal, kept in the repo so the driver is
# reproducible rather than a thing somebody once typed on one machine.
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
