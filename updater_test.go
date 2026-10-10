package main

import "testing"

func TestIsNewer(t *testing.T) {
	for _, tt := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.1.5", "v0.1.4", true},
		{"v0.1.10", "v0.1.9", true},
		{"v0.2.0", "v0.1.99", true},
		{"v0.1.4", "v0.1.4", false},
		{"v0.1.3", "v0.1.4", false},
		{"v0.1.1", "dev", true},
	} {
		if got := isNewer(tt.latest, tt.current); got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}
