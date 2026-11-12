package db

import "testing"

func TestNormalizeSeverity(t *testing.T) {
	for in, want := range map[string]string{"high": "high", " Critical\n": "critical", "LOW": "low", "info": "", "": "", "high|x": "", "\U0001F680": ""} {
		if got := NormalizeSeverity(in); got != want {
			t.Errorf("NormalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
