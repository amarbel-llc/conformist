package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsVersionLine(t *testing.T) {
	var out bytes.Buffer

	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != versionLine() {
		t.Fatalf("run printed %q, want %q", got, versionLine())
	}
}
