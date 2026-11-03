package workitem

import (
	"time"
)

// --- the driver's pulse ---------------------------------------------------
//
// A COMPANION document, kept by the backend (store.Companions): on the
// filesystem it is work/.last-sweep, plain RFC3339 text beside the
// directories it reports on, and therefore answerable with `cat` — Design
// Principle 1 applies to the driver's own liveness as much as to an item's
// status.

// RecordSweep stamps the moment the sweep ran. It is called ONLY by the
// `mg schedule` command, never by Done's internal sweep: the question this
// stamp answers is "is something driving the sweep on a clock", and a sweep
// that happened because somebody finished a ticket is not an answer to it.
func RecordSweep(root string) error {
	c, err := companionsOf(root)
	if err != nil {
		return err
	}
	return c.WriteSweepStamp([]byte(snoozeNow().Format(time.RFC3339) + "\n"))
}

// readSweepStamp reads the stamp RecordSweep wrote.
func readSweepStamp(root string) ([]byte, error) {
	c, err := companionsOf(root)
	if err != nil {
		return nil, err
	}
	return c.ReadSweepStamp()
}
