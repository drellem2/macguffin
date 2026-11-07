package workitem

import (
	"encoding/hex"
	"strings"
	"testing"
)

// isHexID reports whether id is prefix followed by exactly width hex chars.
func isHexID(id, prefix string, width int) bool {
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	rest := id[len(prefix):]
	if len(rest) != width {
		return false
	}
	_, err := hex.DecodeString(rest + strings.Repeat("0", len(rest)%2))
	return err == nil
}

// TestCreate_MintWidthDefault: with MG_ID_HEX_LEN unset (or empty), Create mints
// at the historical default of 5.
func TestCreate_MintWidthDefault(t *testing.T) {
	for _, v := range []string{"", "  "} {
		t.Setenv(idHexLenEnv, v)
		root := t.TempDir()
		setupDirs(t, root)
		item, err := Create(root, "mg-", "task", "default width "+v, nil)
		if err != nil {
			t.Fatalf("MG_ID_HEX_LEN=%q: Create: %v", v, err)
		}
		if !isHexID(item.ID, "mg-", idHexLen) {
			t.Errorf("MG_ID_HEX_LEN=%q: id %q, want mg- plus %d hex chars", v, item.ID, idHexLen)
		}
	}
}

// TestCreate_MintWidthFollowsEnv: the minted width is exactly MG_ID_HEX_LEN,
// across the whole legal range, and the wider id extends the default-width one
// minted from the same (title, created) — only the kept length changes.
func TestCreate_MintWidthFollowsEnv(t *testing.T) {
	pinClock(t, fixedTime())
	const title = "width follows env"
	def := GenerateID("mg-", title, fixedTime())
	for _, w := range []string{"4", "8", " 12 ", "64"} {
		t.Setenv(idHexLenEnv, w)
		want, _ := mintHexLen()
		root := t.TempDir()
		setupDirs(t, root)
		item, err := Create(root, "mg-", "task", title, nil)
		if err != nil {
			t.Fatalf("MG_ID_HEX_LEN=%q: Create: %v", w, err)
		}
		if !isHexID(item.ID, "mg-", want) {
			t.Fatalf("MG_ID_HEX_LEN=%q: id %q, want mg- plus %d hex chars", w, item.ID, want)
		}
		short, long := def, item.ID
		if len(short) > len(long) {
			short, long = long, short
		}
		if !strings.HasPrefix(long, short) {
			t.Errorf("MG_ID_HEX_LEN=%q: id %q and default-width %q are not prefixes of one digest", w, item.ID, def)
		}
		// The minted item is resolvable by its exact id.
		if _, err := Resolve(root, item.ID); err != nil {
			t.Errorf("MG_ID_HEX_LEN=%q: Resolve(%q): %v", w, item.ID, err)
		}
	}
}

// TestCreate_MintWidthRemintsAtConfiguredWidth: the pinned-clock collision
// remint keeps the configured width rather than falling back to the default.
func TestCreate_MintWidthRemintsAtConfiguredWidth(t *testing.T) {
	t.Setenv(idHexLenEnv, "8")
	root := t.TempDir()
	setupDirs(t, root)
	pinClock(t, fixedTime())
	const title = "collide wide"
	first, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("remint reused %q", first.ID)
	}
	for _, id := range []string{first.ID, second.ID} {
		if !isHexID(id, "mg-", 8) {
			t.Errorf("id %q, want mg- plus 8 hex chars", id)
		}
	}
}

// TestCreate_MintWidthInvalidIsError: an invalid MG_ID_HEX_LEN refuses the
// filing with a usage error and writes nothing — never a silent default.
func TestCreate_MintWidthInvalidIsError(t *testing.T) {
	for _, v := range []string{"3", "65", "0", "-5", "eight", "5.5", "0x8"} {
		t.Setenv(idHexLenEnv, v)
		root := t.TempDir()
		setupDirs(t, root)
		item, err := Create(root, "mg-", "task", "bad width", nil)
		if err == nil {
			t.Fatalf("MG_ID_HEX_LEN=%q: Create succeeded with id %q, want an error", v, item.ID)
		}
		me := mgerrOf(t, err)
		if me.Code != "invalid_id_hex_len" {
			t.Errorf("MG_ID_HEX_LEN=%q: code = %q, want invalid_id_hex_len", v, me.Code)
		}
		if !strings.Contains(me.Message, idHexLenEnv) {
			t.Errorf("MG_ID_HEX_LEN=%q: message %q does not name the variable", v, me.Message)
		}
		if items, err := List(root); err == nil && len(items) != 0 {
			t.Errorf("MG_ID_HEX_LEN=%q: a refused filing left %d items behind", v, len(items))
		}
	}
}
