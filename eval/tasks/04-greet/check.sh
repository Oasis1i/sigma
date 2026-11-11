#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > greet_test.go << 'EOF'
package greet

import "testing"

func TestGreet(t *testing.T) {
	if Greet("Ada") != "Hello, Ada!" {
		t.Fatal(Greet("Ada"))
	}
}
EOF
go test
