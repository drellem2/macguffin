package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRootPrecedence pins the resolution order: --root > $MG_ROOT > $HOME.
// Each case asserts the *resolved path*, not which branch ran.
func TestRootPrecedence(t *testing.T) {
	home := t.TempDir()
	env := t.TempDir()
	flag := t.TempDir()

	tests := []struct {
		name     string
		override string
		envVal   string
		setEnv   bool
		want     string
	}{
		{
			name: "no override falls back to home",
			want: filepath.Join(home, ".macguffin"),
		},
		{
			name:   "env beats home",
			envVal: env,
			setEnv: true,
			want:   env,
		},
		{
			name:     "flag beats env",
			override: flag,
			envVal:   env,
			setEnv:   true,
			want:     flag,
		},
		{
			name:     "flag beats home when env unset",
			override: flag,
			want:     flag,
		},
		{
			// An exported-but-empty MG_ROOT must not resolve the store to the
			// process working directory — it falls through to $HOME.
			name:   "empty env falls through to home",
			envVal: "",
			setEnv: true,
			want:   filepath.Join(home, ".macguffin"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			// t.Setenv records the pre-test value so cleanup restores it even
			// for the unset case; a bare os.Unsetenv would leak across subtests.
			t.Setenv(EnvRoot, tt.envVal)
			if !tt.setEnv {
				os.Unsetenv(EnvRoot)
			}

			got, err := Root(tt.override)
			if err != nil {
				t.Fatalf("Root(%q) failed: %v", tt.override, err)
			}
			if got != tt.want {
				t.Errorf("Root(%q) = %q, want %q", tt.override, got, tt.want)
			}
		})
	}
}

// TestRootIsAbsolute: a relative override is anchored at the process working
// directory once, at resolution time. A store path must never stay relative —
// a later chdir would silently move the store.
func TestRootIsAbsolute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("flag", func(t *testing.T) {
		t.Setenv(EnvRoot, "")
		os.Unsetenv(EnvRoot)
		got, err := Root("scratch")
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		if want := filepath.Join(wd, "scratch"); got != want {
			t.Errorf("Root(\"scratch\") = %q, want %q", got, want)
		}
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv(EnvRoot, "scratch")
		got, err := Root("")
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		if want := filepath.Join(wd, "scratch"); got != want {
			t.Errorf("Root(\"\") with %s=scratch = %q, want %q", EnvRoot, got, want)
		}
	})
}

// TestDefaultRootIgnoresOverrides guards the split: DefaultRoot is the home-only
// leaf of the precedence chain and must stay blind to $MG_ROOT, so that Root()
// remains the single place precedence is decided.
func TestDefaultRootIgnoresOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvRoot, t.TempDir())

	got, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot() failed: %v", err)
	}
	if want := filepath.Join(home, ".macguffin"); got != want {
		t.Errorf("DefaultRoot() = %q, want %q (must ignore %s)", got, want, EnvRoot)
	}
}

// TestInitHonoursEnvRoot: Init("") means "the resolved root", not "$HOME".
func TestInitHonoursEnvRoot(t *testing.T) {
	home := t.TempDir()
	env := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvRoot, env)

	if err := Init(""); err != nil {
		t.Fatalf("Init(\"\") failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env, "work", "available")); err != nil {
		t.Errorf("Init(\"\") did not populate %s: %v", env, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".macguffin")); !os.IsNotExist(err) {
		t.Errorf("Init(\"\") touched $HOME/.macguffin despite %s being set", EnvRoot)
	}
}
