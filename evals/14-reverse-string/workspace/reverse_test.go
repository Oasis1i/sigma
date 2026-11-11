package reverse

import "testing"

func TestReverse(t *testing.T) {
	if got := Reverse("ab"); got != "ba" {
		t.Fatalf("Reverse(ab) = %q", got)
	}
	if got := Reverse("sigma"); got != "amgis" {
		t.Fatalf("Reverse(sigma) = %q", got)
	}
	if got := Reverse(""); got != "" {
		t.Fatalf("Reverse(empty) = %q", got)
	}
}
