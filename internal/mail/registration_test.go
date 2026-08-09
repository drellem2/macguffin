package mail

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// regRoot returns an empty mail root for a registration test.
func regRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "mail")
}

func TestRegisterCreatesTheMaildirAndTheRecord(t *testing.T) {
	root := regRoot(t)

	if IsRegistered(root, "crew") {
		t.Fatalf("a box that does not exist cannot be registered")
	}
	if err := Register(root, "crew", Registration{RegisteredBy: "op", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !MailboxExists(root, "crew") {
		t.Errorf("Register must create the maildir")
	}
	if !IsRegistered(root, "crew") {
		t.Errorf("Register must leave a record")
	}
	rec, ok := ReadRegistration(root, "crew")
	if !ok || rec == nil {
		t.Fatalf("ReadRegistration = %v, %v; want a record", rec, ok)
	}
	if rec.Mailbox != "crew" || rec.RegisteredBy != "op" || rec.Via != "register" {
		t.Errorf("record = %+v, want mailbox=crew by=op via=register", rec)
	}
	if rec.RegisteredAt == "" {
		t.Errorf("record must be stamped with a time, got %+v", rec)
	}
	if rec.Adopted {
		t.Errorf("a box that did not exist cannot be adopted, got %+v", rec)
	}
}

// TestRegisterRefusesToOverwrite: the record's value is naming the FIRST
// deliberate act. A second registration that replaced who and when would erase
// the only copy of it, so it is refused rather than applied — and the refusal
// is a distinguishable error, not a generic failure, because the caller's
// correct response is to carry on.
func TestRegisterRefusesToOverwrite(t *testing.T) {
	root := regRoot(t)

	if err := Register(root, "crew", Registration{RegisteredBy: "first", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := Register(root, "crew", Registration{RegisteredBy: "second", Via: "send --create"}, false, 0)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register = %v, want ErrAlreadyRegistered", err)
	}

	rec, _ := ReadRegistration(root, "crew")
	if rec == nil || rec.RegisteredBy != "first" || rec.Via != "register" {
		t.Errorf("the original record must survive, got %+v", rec)
	}
}

// TestRegisterAdoptionRecordsWhatItDoesNotVouchFor: adopting a box already in
// use is a statement about the NAME going forward. The mail already there is
// counted precisely so the record cannot be mistaken for a claim about it.
func TestRegisterAdoptionRecordsWhatItDoesNotVouchFor(t *testing.T) {
	root := regRoot(t)
	if err := EnsureMaildir(root, "daniel"); err != nil {
		t.Fatalf("EnsureMaildir: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := Send(root, "daniel", "someagent", "s", "b"); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	if err := Register(root, "daniel", Registration{RegisteredBy: "op", Via: "register"}, true, 3); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rec, _ := ReadRegistration(root, "daniel")
	if rec == nil || !rec.Adopted || rec.PriorMessages != 3 {
		t.Errorf("record = %+v, want adopted with prior_messages=3", rec)
	}

	// Registration touches no mail.
	msgs, _, err := List(root, "daniel")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("registration changed the mailbox: %d unread, want 3", len(msgs))
	}
}

// TestIsRegisteredSurvivesADamagedRecord: PRESENCE is the fact, the contents
// are detail. A record that cannot be parsed must not read as "never
// registered" — that turns a corrupted file into a silent retraction of the
// very fact it was written to record, which is the defect this file exists to
// remove, wearing a different hat.
func TestIsRegisteredSurvivesADamagedRecord(t *testing.T) {
	root := regRoot(t)
	if err := Register(root, "crew", Registration{RegisteredBy: "op", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := os.WriteFile(RegistrationPath(root, "crew"), []byte("{ truncated"), 0o644); err != nil {
		t.Fatalf("damaging the record: %v", err)
	}

	if !IsRegistered(root, "crew") {
		t.Errorf("a damaged record must still count as registered")
	}
	rec, ok := ReadRegistration(root, "crew")
	if !ok {
		t.Errorf("ok must stay true for a damaged record")
	}
	if rec != nil {
		// A zero-valued Registration would read as one registered by "" at "",
		// which is a confident wrong answer where nil is an honest missing one.
		t.Errorf("a damaged record must yield nil detail, not a blank record: %+v", rec)
	}
	// Still refused: the box IS registered, damage or no damage.
	if err := Register(root, "crew", Registration{RegisteredBy: "second"}, false, 0); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("Register over a damaged record = %v, want ErrAlreadyRegistered", err)
	}
}

// TestRegistrationRecordIsNotMail: the record lives inside the mailbox
// directory, where the listing paths walk. If it were ever counted as a
// message, this change would have invented a phantom mail in every box it
// touched — and phantom mail is what it is here to remove.
func TestRegistrationRecordIsNotMail(t *testing.T) {
	root := regRoot(t)
	if err := Register(root, "crew", Registration{RegisteredBy: "op", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register: %v", err)
	}

	for _, tc := range []struct {
		name string
		fn   func(string, string) ([]Message, int, error)
	}{
		{"List", List},
		{"ListAll", ListAll},
		{"ListArchived", ListArchived},
	} {
		msgs, malformed, err := tc.fn(root, "crew")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(msgs) != 0 || malformed != 0 {
			t.Errorf("%s counted the registration record: %d messages, %d malformed", tc.name, len(msgs), malformed)
		}
	}

	boxes, err := ListMailboxes(root)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	if len(boxes) != 1 || boxes[0].Name != "crew" || boxes[0].Unread != 0 {
		t.Errorf("ListMailboxes = %+v, want exactly one empty box named crew", boxes)
	}
}

// TestMergeMailboxCarriesRegistration: migrate removes the stray box wholesale,
// record included. Without carrying it the migration would silently retract a
// registration somebody performed — turning a vouched-for name into an
// unregistered one as a side effect of tidying, which is the same disappearing
// evidence the record exists to stop.
func TestMergeMailboxCarriesRegistration(t *testing.T) {
	root := regRoot(t)
	if err := Register(root, "mg-d639", Registration{RegisteredBy: "op", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := Send(root, "mg-d639", "mayor", "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	res, err := MergeMailbox(root, "mg-d639", "d639")
	if err != nil {
		t.Fatalf("MergeMailbox: %v", err)
	}
	if res.Moved != 1 {
		t.Errorf("Moved = %d, want 1", res.Moved)
	}
	if !IsRegistered(root, "d639") {
		t.Fatalf("the canonical box must inherit the stray's registration")
	}
	rec, _ := ReadRegistration(root, "d639")
	if rec == nil || rec.RegisteredBy != "op" {
		t.Errorf("carried record = %+v, want registered_by=op", rec)
	}
	if rec != nil && !rec.Adopted {
		t.Errorf("the carried record describes a box that already held mail, so it is an adoption: %+v", rec)
	}
	if IsRegistered(root, "mg-d639") {
		t.Errorf("the stray box must be gone, record and all")
	}
}

// TestMergeMailboxKeepsDestinationRegistration: the destination's record names
// the first deliberate act for the name that SURVIVES. The stray's is about a
// spelling that is going away, so it must not overwrite it.
func TestMergeMailboxKeepsDestinationRegistration(t *testing.T) {
	root := regRoot(t)
	if err := Register(root, "mg-d639", Registration{RegisteredBy: "stray-owner", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register stray: %v", err)
	}
	if err := Register(root, "d639", Registration{RegisteredBy: "real-owner", Via: "register"}, false, 0); err != nil {
		t.Fatalf("Register canonical: %v", err)
	}
	if _, err := Send(root, "mg-d639", "mayor", "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := MergeMailbox(root, "mg-d639", "d639"); err != nil {
		t.Fatalf("MergeMailbox: %v", err)
	}
	rec, _ := ReadRegistration(root, "d639")
	if rec == nil || rec.RegisteredBy != "real-owner" {
		t.Errorf("record = %+v, want the destination's own registered_by=real-owner", rec)
	}
}

// TestMergeMailboxLeavesAnUnregisteredPairUnregistered: migrate must not
// manufacture a registration either. Two boxes nobody established merge into
// one box nobody established.
func TestMergeMailboxLeavesAnUnregisteredPairUnregistered(t *testing.T) {
	root := regRoot(t)
	if _, err := Send(root, "mg-d639", "mayor", "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := MergeMailbox(root, "mg-d639", "d639"); err != nil {
		t.Fatalf("MergeMailbox: %v", err)
	}
	if IsRegistered(root, "d639") {
		t.Errorf("migrating two unregistered boxes must not invent a registration")
	}
}
