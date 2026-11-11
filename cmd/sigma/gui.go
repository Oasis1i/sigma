package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/gui"
)

// runGUI serves the embedded page on 127.0.0.1 and opens a browser. The
// process stays up until Ctrl-C. The token in the printed URL is the only
// credential; it is different every launch.
func runGUI(eng *engine.Engine, port int) error {
	token, err := gui.NewToken()
	if err != nil {
		return err
	}
	srv, err := gui.Listen(eng, token, version, port)
	if err != nil {
		return err
	}
	defer srv.Close()
	fmt.Fprintf(os.Stderr, "sigma: %s @ %s\n", eng.ModelName(), eng.Workspace())
	fmt.Fprintf(os.Stderr, "sigma: gui %s\n", srv.URL())
	fmt.Fprintf(os.Stderr, "sigma: listening on 127.0.0.1 only; the token in that URL is required\n")
	openBrowser(srv.URL())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return srv.Serve(ctx)
}

// openBrowser is best-effort. The URL is already on stderr, so a missing
// opener is not a failure. xdg-open, open, and rundll32 do not link cgo.
func openBrowser(raw string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "sigma: could not open a browser (%v); use the URL above\n", err)
	}
}
