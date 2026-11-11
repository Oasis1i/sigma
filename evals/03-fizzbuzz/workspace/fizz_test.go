package fizz

import "testing"

func TestFizzBuzz(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1"},
		{3, "Fizz"},
		{5, "Buzz"},
		{15, "FizzBuzz"},
	}
	for _, tc := range cases {
		if got := FizzBuzz(tc.n); got != tc.want {
			t.Fatalf("FizzBuzz(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
