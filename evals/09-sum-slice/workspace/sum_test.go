package sum

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{1, 2, 3}); got != 6 {
		t.Fatalf("Sum = %d, want 6", got)
	}
	if got := Sum(nil); got != 0 {
		t.Fatalf("Sum(nil) = %d, want 0", got)
	}
}
