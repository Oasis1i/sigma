#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > pal_test.go << 'EOF'
package pal

import "testing"

func TestPal(t *testing.T) {
	if !IsPalindrome("aba") || IsPalindrome("ab") || !IsPalindrome("") {
		t.Fatal(IsPalindrome("aba"), IsPalindrome("ab"))
	}
}
EOF
go test
