#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > contains_test.go << 'EOF'
package contains

import "testing"

func TestContains(t *testing.T) {
	if !Contains("abc", "b") || Contains("abc", "z") || !Contains("abc", "") {
		t.Fatal(Contains("abc", "b"), Contains("abc", "z"), Contains("abc", ""))
	}
}
EOF
go test
