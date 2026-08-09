package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// A mailbox that exists is not evidence that anyone MEANT it to.
//
// `mg mail send` refuses an unknown recipient (mailregistry.go), which stops a
// typo minting a dead drop. That refusal fires exactly once per name: the
// moment a box exists, every later send to it is accepted, because existence is
// the thing the refusal consults. So a name talked past the guard once — with
// --create, which is the documented escape and therefore the reachable one — is
// promoted to a permanently good address, and nothing on disk afterwards
// separates it from a name somebody deliberately established.
//
// That is not hypothetical. The `daniel` mailbox is in daily use, receiving
// real mail from several agents, and was never registered: it exists because
// mail was delivered to it back when delivery created boxes. It works. "It
// works" is precisely the evidence that is missing — a box in use looks
// identical to a box that was set up, so "did the registration step happen?" had
// no answer at all, let alone a wrong one.
//
// A registration record is that answer. It is written by the deliberate act
// (`mg mail register`, or `send --create`) and never by delivery, so its
// PRESENCE means a person or agent asserted this name, and its ABSENCE means
// the box got here some other way. It records who and when, so the assertion is
// attributable after the fact rather than merely counted.
//
// What it is NOT: an authorization check. Sends to an unregistered box are not
// refused and must not be — 1361 boxes predate this file, and a store-wide
// refusal would break every one of them to punish a bookkeeping gap they had no
// way to close. The record makes the gap VISIBLE (`mg mail list` marks
// unregistered boxes; --json carries the standing) and closable (`mg mail
// register` adopts a box already in use). Enforcement, if it is ever wanted, is
// a separate decision that needs this record to exist first.

// registrationFile is the record's name inside the mailbox directory. It sits
// beside tmp/new/cur rather than inside them: ListMailboxes enumerates
// directories only and every listing walks a named subdirectory, so a plain
// file at the top level is invisible to both and cannot be mistaken for mail.
const registrationFile = ".registration.json"

// Registration is the durable record that a mailbox was deliberately
// established. Every field is descriptive — nothing reads it to make a
// decision — so an unparseable record costs detail, never the fact itself
// (see ReadRegistration).
type Registration struct {
	// Mailbox is the canonical box name the record was written for. It
	// duplicates the directory it lives in, which is what lets a record found
	// loose (copied, restored from a backup, carried by a migration) still say
	// what it describes.
	Mailbox string `json:"mailbox"`
	// RegisteredAt is RFC3339, in UTC.
	RegisteredAt string `json:"registered_at"`
	// RegisteredBy is the acting agent: MG_ACTOR, else POGO_AGENT_NAME, else
	// the OS user. Self-asserted and forgeable, exactly as the work-item audit
	// log's actor is — attribution here is a lead, not a proof.
	RegisteredBy string `json:"registered_by"`
	// Via is the spelling that registered the box: "register", "send --create"
	// or "reply --create". A box registered by --create was established in the
	// act of talking past a refusal, which is worth being able to find later.
	Via string `json:"via"`
	// Adopted is true when the box already existed at registration time — the
	// `daniel` case. The registration is then a statement about the name going
	// forward, NOT a claim that this agent created the box, and the two must
	// not be allowed to read alike.
	Adopted bool `json:"adopted"`
	// PriorMessages counts the messages already in the box when it was
	// adopted. It is the size of what the record does not vouch for.
	PriorMessages int `json:"prior_messages,omitempty"`
}

// ErrAlreadyRegistered reports that a mailbox already carries a registration
// record. Write refuses rather than overwriting: the record's whole value is
// naming the FIRST deliberate act, and a re-registration that silently replaced
// who and when would erase the only copy of it.
var ErrAlreadyRegistered = errors.New("mailbox already registered")

// RegistrationPath returns the path of a mailbox's registration record. It does
// not check that either exists.
func RegistrationPath(mailRoot, agent string) string {
	return filepath.Join(mailRoot, agent, registrationFile)
}

// IsRegistered reports whether a mailbox carries a registration record.
//
// PRESENCE is the fact. The contents are not consulted, so a record that is
// truncated, half-written or written by a later version still answers "yes,
// somebody registered this" — the answer that matters — rather than degrading
// to the "no" that would quietly re-open the question this file closes.
func IsRegistered(mailRoot, agent string) bool {
	if !validComponent(agent) {
		return false
	}
	info, err := os.Stat(RegistrationPath(mailRoot, agent))
	return err == nil && info.Mode().IsRegular()
}

// ReadRegistration returns a mailbox's registration record.
//
// ok reports REGISTRATION, which is the presence of the record; rec carries the
// detail, and is nil when there is no record AND when the record cannot be
// parsed. The two are deliberately independent: a caller asking "is this box
// registered" gets an answer that no amount of damage to the file can flip,
// and a caller wanting who-and-when is told plainly that it is unavailable
// instead of being handed a zero-valued Registration that reads like a real one
// registered by "" at "".
func ReadRegistration(mailRoot, agent string) (rec *Registration, ok bool) {
	if !IsRegistered(mailRoot, agent) {
		return nil, false
	}
	data, err := os.ReadFile(RegistrationPath(mailRoot, agent))
	if err != nil {
		return nil, true
	}
	var r Registration
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, true
	}
	return &r, true
}

// Register writes a mailbox's registration record, creating the Maildir if it
// is not there yet. It returns ErrAlreadyRegistered if a record is already
// present, and it never overwrites one.
//
// The write is atomic and it is LOUD. Delivery has a best-effort audit log
// (audit.go) whose failures are swallowed, because losing an audit line must
// not lose a message; a registration is the opposite — the record IS the
// deliverable, and a register that reported success while writing nothing would
// reproduce, in its own remedy, the exact defect it exists to remove: a step
// that appears to have happened with nothing on disk to show for it.
//
// prior is stamped into the record when the box already existed, so an adoption
// says how much mail it inherited without vouching for it. Callers pass the
// count they measured before calling; it is not recomputed here.
func Register(mailRoot, agent string, rec Registration, existed bool, prior int) error {
	if err := checkMailbox(agent); err != nil {
		return err
	}
	if IsRegistered(mailRoot, agent) {
		return ErrAlreadyRegistered
	}
	if err := EnsureMaildir(mailRoot, agent); err != nil {
		return err
	}

	rec.Mailbox = agent
	rec.Adopted = existed
	if existed {
		rec.PriorMessages = prior
	} else {
		rec.PriorMessages = 0
	}
	if rec.RegisteredAt == "" {
		rec.RegisteredAt = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding registration for %s: %w", agent, err)
	}
	data = append(data, '\n')

	// tmp/ then link, the same idiom deliverOnce uses for a message: the record
	// becomes visible whole or not at all, and the O_EXCL link means two
	// concurrent registrations cannot both claim to be the first — the loser
	// gets ErrAlreadyRegistered, which is the truth.
	tmpPath := filepath.Join(mailRoot, agent, "tmp", fmt.Sprintf(".registration.%d", os.Getpid()))
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing registration for %s: %w", agent, err)
	}
	defer os.Remove(tmpPath) // best-effort: the record now lives at its own path

	if err := os.Link(tmpPath, RegistrationPath(mailRoot, agent)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrAlreadyRegistered
		}
		return fmt.Errorf("registering %s: %w", agent, err)
	}
	return nil
}
