#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > sum_test.go << 'EOF'
package sum

import "testing"

func TestSum(t *testing.T) {
	if Sum(nil) != 0 || Sum([]int{1, 2, 3}) != 6 || Sum([]int{-2, 2}) != 0 {
		t.Fatal(Sum([]int{1, 2, 3}))
	}
}
EOF
go test
