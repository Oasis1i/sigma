package sandbox

import (
	"strings"
	"testing"
)

func TestDockerRunArgs(t *testing.T) {
	d := Docker{Image: "golang:1.22", Dir: "/work/app"}
	args := d.RunArgs("sigma-1", "go test ./...")
	got := strings.Join(args, "\n")
	for _, want := range []string{
		"run",
		"--rm",
		"--name",
		"sigma-1",
		"-v",
		"/work/app:/workspace",
		"-w",
		"/workspace",
		"golang:1.22",
		"bash",
		"-c",
		"go test ./...",
	} {
		if !containsArg(args, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if containsArg(args, "--privileged") || containsArg(args, "--network") {
		t.Fatalf("unexpected flag in %v", args)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
