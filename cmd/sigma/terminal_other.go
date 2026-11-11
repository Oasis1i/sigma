//go:build !linux

package main

// isTerminal is conservative off Linux: ask mode denies mutating tools
// instead of prompting, and --mode auto still runs them.
func isTerminal(int) bool { return false }
