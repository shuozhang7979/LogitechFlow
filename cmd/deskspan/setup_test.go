package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWizardOnlyCommitsConfirmedPreview(t *testing.T) {
	// Alphabetical device list: 1=Mac, 2=Windows. Place Windows on the left.
	for _, tc := range []struct {
		input  string
		writes int
		fail   bool
	}{
		{"1\n2 1\ny\n", 1, false},
		{"2\n2 1\n\n", 0, false},
		{"1\n2 1\n", 0, false},
		{"1\n2 1 2\ny\n", 0, true},
		{"1\n2 3\ny\n", 0, true},
	} {
		writes := 0
		query := func(out any, args ...string) error {
			if args[0] == "call" {
				writes++
			}
			*out.(*arrangeLayout) = twoDesktops()
			return nil
		}
		var output bytes.Buffer
		err := setupSession(query, strings.NewReader(tc.input), &output)
		if (err != nil) != tc.fail || writes != tc.writes {
			t.Errorf("input %q: writes=%d, error=%v", tc.input, writes, err)
		}
	}
}
