package search

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// LoadAPIKey reads the search provider's credential from a file.
//
// A FILE, and only a file. This is the strictest pattern in the platform —
// agentd takes its provider credential as --provider-key-file and its
// signing key as --sts-client-key-file, never as a value — and the reasons
// are all present here:
//
//   - A flag value is world-readable in /proc and in `ps`. Anything that
//     can list processes on the host can read a key passed as --api-key.
//   - An environment variable's value is inherited by every child process
//     and is dumped by half of everything that writes a crash report. The
//     variable this service reads, SEARCHD_API_KEY_FILE, holds a PATH.
//   - A file has an owner and a mode, which is the only part of this a
//     deployment can actually enforce.
//
// The value goes into a Searcher and nowhere else: it is never put in a
// URL, never logged, never returned to a caller, and never named in an
// error — every error below says the PATH, which the operator typed, and
// nothing of the contents.
func LoadAPIKey(path string) (string, error) {
	if path == "" {
		return "", errors.New("no API key file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("api key: %w", err)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", fmt.Errorf("api key: %s is empty", path)
	}
	// A key with a space or a control character in the middle is a file
	// somebody put something else in — a whole curl command, a YAML
	// fragment, an export line. It would go into a request header, where a
	// newline is a header injection and everything else is a silent 401.
	for _, r := range key {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("api key: %s holds whitespace or control characters inside the key; it should hold the key and nothing else", path)
		}
	}
	return key, nil
}

// KeyFileWarning returns a sentence an operator should see when the key
// file is readable by more than its owner, or "" when it is not. A warning
// rather than a refusal: a Kubernetes secret is mounted 0644 by default and
// refusing would make the strict thing the thing nobody deploys.
func KeyFileWarning(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Sprintf("the API key file is mode %04o; it is readable by more than its owner", mode)
	}
	return ""
}
