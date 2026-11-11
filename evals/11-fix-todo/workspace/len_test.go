package strlen

import "testing"

func TestLen(t *testing.T) {
	if got := Len("sigma"); got != 5 {
		t.Fatalf("Len = %d, want 5", got)
	}
	if got := Len(""); got != 0 {
		t.Fatalf("Len(empty) = %d, want 0", got)
	}
}
