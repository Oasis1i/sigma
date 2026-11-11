package approval

import "testing"

func TestRuleOrderDenyAskAllow(t *testing.T) {
	// Allow is listed first on purpose. Deny still wins, then ask, then allow.
	p, err := New(string(ModeAuto), []Rule{
		{Action: Allow, Pattern: "bash:*"},
		{Action: Ask, Pattern: "bash:git push*"},
		{Action: Deny, Pattern: "bash:rm -rf *"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if d := p.Decide("bash", "rm -rf /tmp", false); d.Action != Deny || d.Rule != "bash:rm -rf *" {
		t.Fatalf("deny should beat allow, got %+v", d)
	}
	if d := p.Decide("bash", "git push origin main", false); d.Action != Ask || d.Rule != "bash:git push*" {
		t.Fatalf("ask should beat allow, got %+v", d)
	}
	if d := p.Decide("bash", "go test ./...", false); d.Action != Allow || d.Rule != "bash:*" {
		t.Fatalf("allow should match other bash, got %+v", d)
	}
	// Anchored: the pattern does not match a command that merely mentions rm.
	if d := p.Decide("bash", "echo rm -rf /tmp", false); d.Action != Allow {
		t.Fatalf("echo should not match the deny rule, got %+v", d)
	}
}

func TestFirstDenyIsReported(t *testing.T) {
	p, err := New(string(ModeAuto), []Rule{
		{Action: Deny, Pattern: "bash:rm *"},
		{Action: Deny, Pattern: "bash:*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := p.Decide("bash", "rm -rf /tmp", false)
	if d.Action != Deny || d.Rule != "bash:rm *" {
		t.Fatalf("got %+v", d)
	}
}

func TestModeDefaults(t *testing.T) {
	plan, err := New("plan", []Rule{{Action: Allow, Pattern: "bash:*"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := plan.Decide("read_file", "main.go", true); d.Action != Allow {
		t.Fatalf("plan read: %+v", d)
	}
	if d := plan.Decide("bash", "ls", false); d.Action != Deny {
		t.Fatalf("plan mode should ignore allow rules for writes, got %+v", d)
	}
	if d := plan.Decide("edit_file", "main.go", false); d.Action != Deny {
		t.Fatalf("plan edit: %+v", d)
	}
	// A deny rule still blocks a read in plan mode.
	planDeny, err := New("plan", []Rule{{Action: Deny, Pattern: "read_file:.env"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := planDeny.Decide("read_file", ".env", true); d.Action != Deny {
		t.Fatalf("deny read: %+v", d)
	}

	ask, err := New("ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := ask.Decide("grep", ".", true); d.Action != Allow || d.Reason != "read-only tool" {
		t.Fatalf("ask read: %+v", d)
	}
	if d := ask.Decide("bash", "ls", false); d.Action != Ask {
		t.Fatalf("ask bash: %+v", d)
	}

	auto, err := New("auto", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := auto.Decide("write_file", "a.txt", false); d.Action != Allow || d.Reason != "auto mode" {
		t.Fatalf("auto write: %+v", d)
	}
}

func TestAlwaysDoesNotOverrideDenyOrAskRules(t *testing.T) {
	p, err := New("ask", []Rule{
		{Action: Deny, Pattern: "bash:rm -rf *"},
		{Action: Ask, Pattern: "bash:git push*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.RememberAlways("bash")

	if d := p.Decide("bash", "rm -rf /", false); d.Action != Deny {
		t.Fatalf("always must not override deny, got %+v", d)
	}
	if d := p.Decide("bash", "git push", false); d.Action != Ask {
		t.Fatalf("always must not override an ask rule, got %+v", d)
	}
	if d := p.Decide("bash", "ls", false); d.Action != Allow || d.Reason != "allowed for this session" {
		t.Fatalf("always should cover the default ask, got %+v", d)
	}
}

func TestGlobPatterns(t *testing.T) {
	p, err := New("ask", []Rule{
		{Action: Allow, Pattern: "bash:go test*"},
		{Action: Deny, Pattern: "write_file:*.env"},
		{Action: Deny, Pattern: "*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The universal deny is last in the file and still wins.
	if d := p.Decide("bash", "go test ./...", false); d.Action != Deny || d.Rule != "*" {
		t.Fatalf("got %+v", d)
	}

	p, err = New("ask", []Rule{{Action: Allow, Pattern: "bash:go test*"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Decide("bash", "go test ./...", false); d.Action != Allow {
		t.Fatalf("prefix glob: %+v", d)
	}
	if d := p.Decide("bash", "echo go test", false); d.Action != Ask {
		t.Fatalf("anchored glob should miss, got %+v", d)
	}
	if d := p.Decide("read_file", "go test", true); d.Action != Allow {
		t.Fatalf("tool name should not match a bash rule, and reads are allowed: %+v", d)
	}
}

func TestRejectBadPolicy(t *testing.T) {
	if _, err := New("yolo", nil); err == nil {
		t.Fatal("expected bad mode")
	}
	if _, err := New("auto", []Rule{{Action: "permit", Pattern: "bash"}}); err == nil {
		t.Fatal("expected bad action")
	}
	if _, err := New("auto", []Rule{{Action: Deny, Pattern: "  "}}); err == nil {
		t.Fatal("expected empty pattern")
	}
}
