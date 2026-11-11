package main

import (
	"strings"
	"testing"
)

func TestOutputRequiresOneShot(t *testing.T) {
	err := run([]string{"--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "only valid with -p") {
		t.Fatalf("got %v", err)
	}
	err = run([]string{"-p", "task", "--output", "yaml"})
	if err == nil || !strings.Contains(err.Error(), "text, json, or jsonl") {
		t.Fatalf("got %v", err)
	}
}
