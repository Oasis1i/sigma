package last

import "testing"

func TestLast(t *testing.T) {
	if _, ok := Last(nil); ok {
		t.Fatal("empty slice should report false")
	}
	got, ok := Last([]int{4, 5, 6})
	if !ok || got != 6 {
		t.Fatalf("Last = %d, %v; want 6, true", got, ok)
	}
}
