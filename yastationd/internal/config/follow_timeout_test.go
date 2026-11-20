package config

import (
	"strings"
	"testing"
)

func TestFollowUpListenTimeoutConfig(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"0", true}, {"1", true}, {"15", true}, {"-1", false}, {"16", false},
	} {
		_, err := Parse(strings.NewReader("follow_up_listen_timeout = " + tc.value + "\n"))
		if (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.value, err)
		}
	}
}
