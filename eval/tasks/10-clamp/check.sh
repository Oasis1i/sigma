#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > clamp_test.go << 'EOF'
package clamp

import "testing"

func TestClamp(t *testing.T) {
	if Clamp(5, 0, 3) != 3 || Clamp(-1, 0, 3) != 0 || Clamp(2, 0, 3) != 2 {
		t.Fatal(Clamp(5, 0, 3), Clamp(-1, 0, 3), Clamp(2, 0, 3))
	}
}
EOF
go test
