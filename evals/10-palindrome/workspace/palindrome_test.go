package palindrome

import "testing"

func TestIsPalindrome(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"racecar", true},
		{"hello", false},
		{"a", true},
		{"", true},
	}
	for _, tc := range cases {
		if got := IsPalindrome(tc.in); got != tc.want {
			t.Fatalf("IsPalindrome(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
