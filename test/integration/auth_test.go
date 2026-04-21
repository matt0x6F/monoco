//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
)

// TestChooseCloneURL_NoTokenInURL asserts that even when a token is set,
// chooseCloneURL returns a plain URL — the token travels through the
// .netrc helper exclusively. Regression guard for the security fix.
func TestChooseCloneURL_NoTokenInURL(t *testing.T) {
	token := os.Getenv("MONOCO_TEST_REPO_TOKEN")
	if token == "" {
		t.Skip("MONOCO_TEST_REPO_TOKEN not set; nothing to leak")
	}
	url, _ := chooseCloneURL()
	if strings.Contains(url, "@") {
		t.Errorf("clone URL contains '@' (token embedding?): %q", url)
	}
	if strings.Contains(url, token) {
		t.Errorf("clone URL leaks the token substring")
	}
}

// TestSanitizedEnv_PointsHOMEAtNetrcDir asserts that when a token is
// configured, sanitizedEnv sets HOME to the scratch dir and adds
// GIT_CONFIG_GLOBAL=/dev/null so the token flows via .netrc only.
func TestSanitizedEnv_PointsHOMEAtNetrcDir(t *testing.T) {
	token := os.Getenv("MONOCO_TEST_REPO_TOKEN")
	if token == "" {
		t.Skip("MONOCO_TEST_REPO_TOKEN not set")
	}
	env := sanitizedEnv()
	var gotHome, gotCfg bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			if gotHome {
				t.Errorf("duplicate HOME= in env: %v", env)
			}
			gotHome = true
			home := strings.TrimPrefix(kv, "HOME=")
			netrc := home + "/.netrc"
			fi, err := os.Stat(netrc)
			if err != nil {
				t.Errorf("stat %s: %v", netrc, err)
				continue
			}
			if mode := fi.Mode().Perm(); mode != 0o600 {
				t.Errorf(".netrc mode = %o, want 0600", mode)
			}
		}
		if strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") {
			gotCfg = true
		}
		if strings.Contains(kv, token) && !strings.HasPrefix(kv, "MONOCO_TEST_REPO_TOKEN=") {
			t.Errorf("env var leaks token: %s", kv)
		}
	}
	if !gotHome {
		t.Errorf("sanitizedEnv did not set HOME")
	}
	if !gotCfg {
		t.Errorf("sanitizedEnv did not set GIT_CONFIG_GLOBAL")
	}
}
