#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > double_test.go << 'EOF'
package double

import "testing"

func TestDouble(t *testing.T) {
	if Double(4) != 8 || Double(0) != 0 || Double(-3) != -6 {
		t.Fatal(Double(4), Double(0), Double(-3))
	}
}
EOF
go test
