package mgerr

import (
	"errors"
	"fmt"
	"testing"
)

// TestCategoryExitCode locks the FROZEN exit-code contract (§1). If any of
// these change, a downstream shell consumer breaks — that is the whole point of
// the contract, so this test is intentionally rigid.
func TestCategoryExitCode(t *testing.T) {
	cases := []struct {
		cat  Category
		exit int
	}{
		{CatInternal, 1},
		{CatUsage, 2},
		{CatNotFound, 3},
		{CatConflict, 4},
		{Category(99), 1}, // out-of-range degrades to internal, never 0
	}
	for _, c := range cases {
		if got := c.cat.ExitCode(); got != c.exit {
			t.Errorf("Category(%d).ExitCode() = %d, want %d", c.cat, got, c.exit)
		}
	}
}

// TestCategoryString locks the FROZEN JSON category names (§4).
func TestCategoryString(t *testing.T) {
	cases := []struct {
		cat  Category
		name string
	}{
		{CatInternal, "internal"},
		{CatUsage, "usage"},
		{CatNotFound, "not_found"},
		{CatConflict, "conflict"},
		{Category(99), "internal"},
	}
	for _, c := range cases {
		if got := c.cat.String(); got != c.name {
			t.Errorf("Category(%d).String() = %q, want %q", c.cat, got, c.name)
		}
	}
}

func TestConstructors(t *testing.T) {
	u := Usage("mutually_exclusive_flags", "bad flags", "drop one")
	if u.Category != CatUsage || u.Code != "mutually_exclusive_flags" || u.ExitCode() != 2 {
		t.Errorf("CatUsage constructor: %+v", u)
	}
	n := NotFound("no_such_item", "mg-x: no such work item.", "")
	if n.Category != CatNotFound || n.ExitCode() != 3 {
		t.Errorf("CatNotFound constructor: %+v", n)
	}
	c := Conflict("already_claimed", "mg-x: already claimed.", "release it")
	if c.Category != CatConflict || c.ExitCode() != 4 {
		t.Errorf("CatConflict constructor: %+v", c)
	}
}

// TestErrorReturnsMessageOnly verifies the crux of the two-level scheme: Error()
// is the message WITHOUT the hint. The hint lives in its own field and is
// appended only by the renderer.
func TestErrorReturnsMessageOnly(t *testing.T) {
	e := Conflict("already_claimed", "mg-1234: already claimed (by PID 991).", "See who holds it: mg list --status=claimed")
	if e.Error() != "mg-1234: already claimed (by PID 991)." {
		t.Errorf("Error() = %q, want message only (no hint)", e.Error())
	}
	if e.Hint == "" {
		t.Error("Hint should be carried separately, not flattened into the message")
	}
}

func TestRetryableDefaultsFalse(t *testing.T) {
	if Conflict("already_claimed", "m", "").Retryable {
		t.Error("Retryable should default to false")
	}
	e := Conflict("claim_race", "race", "").WithRetryable(true)
	if !e.Retryable {
		t.Error("WithRetryable(true) should set Retryable")
	}
	if e.ExitCode() != 4 {
		t.Error("retryable does not change the exit code — it is a field, not a code")
	}
}

// TestUnwrapPreservesChain ensures errors.Is/As still work across the typed
// boundary (e.g. a retrofitted mail producer wrapping ErrMalformed).
func TestUnwrapPreservesChain(t *testing.T) {
	sentinel := errors.New("malformed message")
	e := Wrap(CatNotFound, "malformed_message", fmt.Errorf("%w: bad header", sentinel), "")
	if !errors.Is(e, sentinel) {
		t.Error("errors.Is should see the wrapped sentinel through *Error.Unwrap")
	}
	if e.Category != CatNotFound || e.ExitCode() != 3 {
		t.Errorf("Wrap kept wrong category: %+v", e)
	}
}

func TestInternalWrap(t *testing.T) {
	base := errors.New("disk on fire")
	e := Internal(base)
	if e.Category != CatInternal || e.Code != "internal" || e.ExitCode() != 1 {
		t.Errorf("CatInternal: %+v", e)
	}
	if e.Message != "disk on fire" || !errors.Is(e, base) {
		t.Errorf("CatInternal should carry and wrap the cause: %+v", e)
	}
	if Internal(nil).Message != "" {
		t.Error("Internal(nil) should not panic and should have empty message")
	}
}

func TestCoerce(t *testing.T) {
	if Coerce(nil) != nil {
		t.Error("Coerce(nil) should be nil")
	}

	// Already typed: returned as-is.
	orig := Conflict("already_done", "done", "")
	if got := Coerce(orig); got != orig {
		t.Error("Coerce should return an existing *Error unchanged")
	}

	// Typed error nested in a wrap chain is still recovered.
	nested := fmt.Errorf("context: %w", orig)
	if got := Coerce(nested); got != orig {
		t.Error("Coerce should recover a wrapped *Error via errors.As")
	}

	// Cobra usage error → CatUsage/exit 2.
	if got := Coerce(errors.New(`unknown command "foo" for "mg"`)); got.Category != CatUsage || got.ExitCode() != 2 {
		t.Errorf("Coerce(unknown command) = %+v, want CatUsage", got)
	}
	if got := Coerce(errors.New("accepts 1 arg(s), received 0")); got.Category != CatUsage {
		t.Errorf("Coerce(accepts N args) = %+v, want CatUsage", got)
	}

	// Anything else → CatInternal/exit 1 (backward-compat default).
	if got := Coerce(errors.New("something unexpected")); got.Category != CatInternal || got.ExitCode() != 1 {
		t.Errorf("Coerce(unclassified) = %+v, want CatInternal", got)
	}
}
