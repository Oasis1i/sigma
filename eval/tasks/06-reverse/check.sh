#!/bin/sh
set -eu
cd "$WORKSPACE"
cat > reverse_test.go << 'EOF'
package reverse

import "testing"

func TestReverse(t *testing.T) {
	if Reverse("ab") != "ba" || Reverse("a") != "a" || Reverse("") != "" {
		t.Fatal(Reverse("ab"))
	}
}
EOF
go test
