package mul

import "testing"

func TestMul(t *testing.T) {
	if got := Mul(3, 4); got != 12 {
		t.Fatalf("Mul(3, 4) = %d, want 12", got)
	}
	if got := Mul(-2, 5); got != -10 {
		t.Fatalf("Mul(-2, 5) = %d, want -10", got)
	}
}
