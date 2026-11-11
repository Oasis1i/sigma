#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > min_test.go << 'EOF'
package min

import "testing"

func TestMin(t *testing.T) {
	if Min(3, 1) != 1 || Min(1, 3) != 1 || Min(-2, -5) != -5 {
		t.Fatal(Min(3, 1), Min(-2, -5))
	}
}
EOF
go test
