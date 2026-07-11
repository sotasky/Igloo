package main

import (
	"strings"
	"testing"
)

func TestUsage(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run([]string{"help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run help exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"Usage: igloo-dev <command> [args]",
		"lifecycle-audit",
		"persistence-audit",
		"query-audit",
		"sqlite-repack",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run([]string{"missing"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run missing exit=%d want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "missing"`) {
		t.Fatalf("stderr missing unknown command:\n%s", stderr.String())
	}
}
