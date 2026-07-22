package main

import (
	"errors"
	"os"
	"testing"
)

// TestVerbBadFlagReturnsUsageShown pins the single-error-line fix (L4): on a bad
// flag, flag.ContinueOnError already prints the error + usage to stderr, and the
// verb returns errUsageShown so main exits non-zero WITHOUT printing the message
// a redundant second time.
func TestVerbBadFlagReturnsUsageShown(t *testing.T) {
	cases := []struct {
		name string
		args []string
		fn   func([]string) error
	}{
		{"grant", []string{"-no-such-flag"}, cmdGrant},
		{"serve", []string{"-no-such-flag"}, cmdServe},
		{"keys", []string{"set", "-no-such-flag"}, cmdKeys},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := silenceStderr(t)
			err := tc.fn(tc.args)
			restore()
			if !errors.Is(err, errUsageShown) {
				t.Fatalf("%s bad flag = %v, want errUsageShown", tc.name, err)
			}
		})
	}
}

// silenceStderr redirects os.Stderr to the null device for a verb call so the
// flag package's usage dump does not pollute test output. Tests in this package
// run sequentially, so swapping the global is safe.
func silenceStderr(t *testing.T) func() {
	t.Helper()
	orig := os.Stderr
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stderr = devnull
	return func() {
		os.Stderr = orig
		devnull.Close()
	}
}
