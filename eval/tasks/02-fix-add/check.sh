#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > add_test.go << 'EOF'
package add

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("%d", Add(2, 3))
	}
	if Add(-1, 1) != 0 {
		t.Fatal(Add(-1, 1))
	}
}
EOF
go test
