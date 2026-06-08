package config

import (
	"os"
	"testing"
)

// TestMain isolates the user-home lookup for the whole package so config
// tests never read the developer's real ~/.local-review.yml. Pre-fix, every
// test that called Load() merged in whatever the developer had at home,
// making results non-deterministic and (observed) leaking a real provider
// endpoint into `config`-dump test output. os.UserHomeDir() reads $HOME on
// Unix and %USERPROFILE% on Windows, so both are pointed at an empty temp
// dir. Individual tests that need a specific home still override via
// t.Setenv (which restores to this temp dir afterwards).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "lr-config-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
