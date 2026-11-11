package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Oasis1i/sigma/internal/auth"
	"github.com/Oasis1i/sigma/internal/config"
)

func loginCommand(args []string) error {
	fs := flag.NewFlagSet("sigma login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "config file, used only for the subscription opt-in")
	workspace := fs.String("workspace", "", "workspace, used to find .sigma.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sigma login <chatgpt|claude|gemini|grok>")
	}
	name := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	spec, ok := auth.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown provider %q (want chatgpt, claude, gemini, or grok)", name)
	}
	allow, per, err := config.ReadOptIn(*configPath, *workspace)
	if err != nil {
		return err
	}
	if name == "gemini" && (spec.ClientID == "" || spec.ClientSecret == "") {
		return fmt.Errorf("gemini login needs SIGMA_GEMINI_CLIENT_ID and SIGMA_GEMINI_CLIENT_SECRET (the installed-app credentials published by google-gemini/gemini-cli; this repo does not vendor them)")
	}
	if auth.NeedsRisk(name) {
		fmt.Fprintln(os.Stderr, auth.RiskWarning)
		if !config.OptIn(allow, per, name) {
			return fmt.Errorf("refusing to log in to %s until allow_subscription_risk or subscription.%s is true", name, name)
		}
	}
	if name == "grok" && spec.AuthURL == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		authURL, tokenURL, derr := auth.Discover(ctx, nil, auth.GrokDiscovery)
		cancel()
		if derr != nil {
			return fmt.Errorf("grok discovery: %w", derr)
		}
		spec.AuthURL = authURL
		spec.TokenURL = tokenURL
	}
	store, err := auth.DefaultStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_, err = auth.Login(ctx, spec, store, auth.LoginOptions{
		In:  os.Stdin,
		Out: os.Stderr,
		Open: func(rawURL string) error {
			openBrowser(rawURL)
			return nil
		},
	})
	return err
}

func logoutCommand(args []string) error {
	fs := flag.NewFlagSet("sigma logout", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sigma logout <chatgpt|claude|gemini|grok>")
	}
	name := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if _, ok := auth.Lookup(name); !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	store, err := auth.DefaultStore()
	if err != nil {
		return err
	}
	if err := store.Delete(name); err != nil {
		return err
	}
	fmt.Printf("logged out %s\n", name)
	return nil
}

func authCommand(args []string) error {
	fs := flag.NewFlagSet("sigma auth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && fs.Arg(0) != "status") {
		return fmt.Errorf("usage: sigma auth status")
	}
	store, err := auth.DefaultStore()
	if err != nil {
		return err
	}
	for _, name := range auth.Known() {
		st, err := store.Status(name)
		if err != nil {
			return err
		}
		if !st.LoggedIn {
			fmt.Printf("%s\tnot logged in\n", name)
			continue
		}
		who := st.Email
		if who == "" {
			who = "-"
		}
		acct := st.AccountID
		if acct == "" {
			acct = "-"
		}
		exp := "-"
		if !st.Expiry.IsZero() {
			exp = st.Expiry.Format(time.RFC3339)
		}
		fmt.Printf("%s\tlogged in\t%s\t%s\texpires %s\n", name, who, acct, exp)
	}
	return nil
}
