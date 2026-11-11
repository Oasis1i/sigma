package greet

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("sigma"); got != "hello sigma" {
		t.Fatalf("Greet = %q, want %q", got, "hello sigma")
	}
}
