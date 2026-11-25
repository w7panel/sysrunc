package main

import (
	"strings"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestFeaturesForSpec(t *testing.T) {
	for _, tc := range []struct {
		name, executable string
		annotations      map[string]string
		fs, ticker       bool
		errContains      string
	}{
		{name: "new default", executable: "/usr/bin/sysrunc"},
		{name: "legacy full default", executable: "/usr/bin/sysbox-runc", fs: true},
		{name: "legacy lite default", executable: "/usr/bin/sysbox-runc-lite"},
		{name: "explicit full", executable: "sysrunc", annotations: map[string]string{fsAnnotation: "true"}, fs: true},
		{name: "explicit override", executable: "sysbox-runc", annotations: map[string]string{fsAnnotation: "false"}},
		{name: "ticker", executable: "sysrunc", annotations: map[string]string{fsAnnotation: "true", tickerAnnotation: "true"}, fs: true, ticker: true},
		{name: "ticker requires fs", executable: "sysrunc", annotations: map[string]string{tickerAnnotation: "true"}, errContains: "requires"},
		{name: "invalid fs", executable: "sysrunc", annotations: map[string]string{fsAnnotation: "sometimes"}, errContains: fsAnnotation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := featuresForSpec(&specs.Spec{Annotations: tc.annotations}, tc.executable)
			if tc.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error = %v, want %q", err, tc.errContains)
				}
				return
			}
			if err != nil || got.fs != tc.fs || got.ticker != tc.ticker {
				t.Fatalf("features = %+v, %v; want fs=%t ticker=%t", got, err, tc.fs, tc.ticker)
			}
		})
	}
}
