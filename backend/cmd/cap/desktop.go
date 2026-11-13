package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"alfaos/alfad/internal/hostaccess"
)

// desktopTool is what cap desktop needs from hostaccess.Desktop.
type desktopTool interface {
	Health(ctx context.Context) hostaccess.DesktopHealth
	Repair(ctx context.Context) (hostaccess.RepairResult, error)
	Diagnostics(ctx context.Context) string
	ResetFallback(ctx context.Context) error
}

// cmdDesktop: NoCap Desktop from the console, the way in when the screen is
// black and the browser is far away.
func cmdDesktop(ctx context.Context, out io.Writer, d desktopTool, root bool, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: cap desktop status|repair|diagnose|reset-fallback")
	}
	if !root {
		return errors.New("permission denied; run it as root: sudo cap desktop " + args[0])
	}
	printHealth := func(h hostaccess.DesktopHealth) {
		fmt.Fprintf(out, "NoCap Desktop: %s\n", h.Status)
		if h.Fallback != "" {
			fmt.Fprintf(out, "fallback in use: %s (undo: sudo cap desktop reset-fallback)\n", h.Fallback)
		}
		for _, p := range h.Problems {
			if p.Detail != "" {
				fmt.Fprintf(out, "  - %s: %s\n", p.Code, p.Detail)
			} else {
				fmt.Fprintf(out, "  - %s\n", p.Code)
			}
		}
	}
	switch args[0] {
	case "status":
		h := d.Health(ctx)
		printHealth(h)
		if h.Status == "broken" {
			fmt.Fprintln(out, "Repair it with: sudo cap desktop repair")
		}
		return nil
	case "repair":
		res, err := d.Repair(ctx)
		for _, a := range res.Actions {
			fmt.Fprintf(out, "done: %s\n", a)
		}
		if err != nil {
			if errors.Is(err, hostaccess.ErrDesktopUnavailable) {
				return errors.New("NoCap Desktop isn't set up here; run the NoCapOS update (sudo cap update) to set it up")
			}
			return err
		}
		printHealth(res.Health)
		fmt.Fprintln(out, "The sign-in screen restarted. Still black? Send the output of: sudo cap desktop diagnose")
		return nil
	case "diagnose":
		fmt.Fprint(out, d.Diagnostics(ctx))
		return nil
	case "reset-fallback":
		if err := d.ResetFallback(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, "Back to the normal setup (Wayland, hardware rendering); the sign-in screen restarted.")
		return nil
	}
	return fmt.Errorf("unknown option %q; usage: cap desktop status|repair|diagnose|reset-fallback", args[0])
}
