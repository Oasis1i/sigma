#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > last_test.go << 'EOF'
package last

import "testing"

func TestLast(t *testing.T) {
	if Last([]int{1, 2, 9}) != 9 || Last([]int{4}) != 4 {
		t.Fatal(Last([]int{1, 2, 9}))
	}
}
EOF
go test
