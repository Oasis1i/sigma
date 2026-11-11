// Package approval decides whether a tool call may run.
//
// This is separate from package sandbox, which decides where a command runs.
// A later Docker runner can replace local execution without changing the rules.
//
// Rules are not first-match-wins. They are grouped by action and checked in a
// fixed order: deny, then ask, then allow. One matching deny beats an allow
// written earlier in the file. After the rules, plan mode still refuses
// anything that is not read-only, and a session "always" grant does not
// override a deny or an explicit ask rule.
package approval

import (
	"fmt"
	"regexp"
	"strings"
)

// Action is the outcome of a rule or a decision.
type Action string

const (
	Deny  Action = "deny"
	Ask   Action = "ask"
	Allow Action = "allow"
)

// Mode is the default policy for tools that no rule matched.
type Mode string

const (
	ModePlan Mode = "plan"
	ModeAsk  Mode = "ask"
	ModeAuto Mode = "auto"
)

// Rule matches a tool call. Pattern is "tool", "tool:glob", or "*".
// The glob is matched against the whole argument (the command, or the path).
type Rule struct {
	Action  Action
	Pattern string
}

// Decision is what the policy wants to do with one call.
type Decision struct {
	Action Action
	Rule   string // pattern that matched, empty when the mode supplied the default
	Reason string
}

// Policy evaluates rules for one session. It is not safe for concurrent use.
type Policy struct {
	Mode  Mode
	Rules []Rule
	// always remembers tools the user accepted with "always" for this session.
	// It fills in the default for that tool; it does not override deny or ask rules.
	always map[string]bool
}

// New checks the mode and rules and returns a policy.
func New(mode string, rules []Rule) (*Policy, error) {
	p := &Policy{always: map[string]bool{}}
	if err := p.SetMode(mode); err != nil {
		return nil, err
	}
	for _, r := range rules {
		r.Action = Action(strings.TrimSpace(string(r.Action)))
		r.Pattern = strings.TrimSpace(r.Pattern)
		switch r.Action {
		case Deny, Ask, Allow:
		default:
			return nil, fmt.Errorf("invalid rule action %q", r.Action)
		}
		if r.Pattern == "" {
			return nil, fmt.Errorf("rule action %q has an empty pattern", r.Action)
		}
		p.Rules = append(p.Rules, r)
	}
	return p, nil
}

// SetMode changes the default used when no rule matches.
func (p *Policy) SetMode(mode string) error {
	switch Mode(mode) {
	case ModePlan, ModeAsk, ModeAuto:
		p.Mode = Mode(mode)
		return nil
	default:
		return fmt.Errorf("unknown approval mode %q (want plan, ask, or auto)", mode)
	}
}

// RememberAlways lets this tool skip the default ask prompt for the rest of
// the session. Deny rules, plan mode, and explicit ask rules still apply.
func (p *Policy) RememberAlways(tool string) {
	if p.always == nil {
		p.always = map[string]bool{}
	}
	p.always[tool] = true
}

// Decide returns the action for one call. readOnly comes from the tool
// (read_file, list_dir, grep). The argument is the command or the path, not
// the raw JSON.
func (p *Policy) Decide(tool, arg string, readOnly bool) Decision {
	var deny, ask, allow *Rule
	for i := range p.Rules {
		r := &p.Rules[i]
		if !matchRule(r.Pattern, tool, arg) {
			continue
		}
		switch r.Action {
		case Deny:
			if deny == nil {
				deny = r
			}
		case Ask:
			if ask == nil {
				ask = r
			}
		case Allow:
			if allow == nil {
				allow = r
			}
		}
	}
	if deny != nil {
		return Decision{Action: Deny, Rule: deny.Pattern, Reason: "matched deny rule"}
	}
	if p.Mode == ModePlan && !readOnly {
		return Decision{Action: Deny, Reason: "plan mode allows read-only tools only"}
	}
	if ask != nil {
		return Decision{Action: Ask, Rule: ask.Pattern, Reason: "matched ask rule"}
	}
	if allow != nil {
		return Decision{Action: Allow, Rule: allow.Pattern, Reason: "matched allow rule"}
	}
	if p.always[tool] {
		return Decision{Action: Allow, Reason: "allowed for this session"}
	}
	if readOnly {
		return Decision{Action: Allow, Reason: "read-only tool"}
	}
	if p.Mode == ModeAuto {
		return Decision{Action: Allow, Reason: "auto mode"}
	}
	return Decision{Action: Ask, Reason: "ask mode"}
}

// matchRule reports whether pattern matches this tool and argument.
// "*" matches every call. "bash" matches every bash call. "bash:go test*"
// matches bash only when the argument matches the glob.
func matchRule(pattern, tool, arg string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "*" {
		return true
	}
	toolPat := pattern
	argPat := ""
	hasArg := false
	if i := strings.IndexByte(pattern, ':'); i >= 0 {
		toolPat = pattern[:i]
		argPat = pattern[i+1:]
		hasArg = true
	}
	if toolPat == "" || !matchGlob(toolPat, tool) {
		return false
	}
	if !hasArg {
		return true
	}
	return matchGlob(argPat, arg)
}

// matchGlob is a full-string glob. * matches any sequence, including slashes
// and spaces, and ? matches one byte-or-rune as a single regex dot.
func matchGlob(pattern, value string) bool {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(value)
}
