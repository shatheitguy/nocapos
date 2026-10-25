// Command cap is NoCapOS's own small admin CLI: a couple of everyday things
// an admin would otherwise need to look up an apt/journalctl incantation
// for. It runs standalone — it does not need alfad running, and talks to
// the same system packages alfad itself manages (internal/syspkg).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"text/tabwriter"

	"alfaos/alfad/internal/syspkg"
)

var version = "dev" // set via -ldflags "-X main.version=..."

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "update":
		err = cmdUpdate(context.Background())
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("cap " + version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "cap: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cap:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`cap — NoCapOS's command-line admin tool

Usage:
  cap update          Refresh and list available Linux package updates
  cap logs [-f] [N]   Show NoCapOS's own service logs (-f follows, N = line count)
  cap version         Print the cap version

cap update needs root: sudo cap update
`)
}

// cmdUpdate refreshes the package lists (apt-get update or the equivalent
// for this system's package manager) and reports what has a newer version —
// the same check the Packages app runs, from the terminal.
func cmdUpdate(ctx context.Context) error {
	mgr := syspkg.Manager()
	if mgr == "" {
		return errors.New("no supported package manager found on this system (apt, dnf, pacman or zypper)")
	}
	if os.Geteuid() != 0 {
		return errors.New("run as root: sudo cap update")
	}

	admin := syspkg.NewAdmin()
	fmt.Println("Refreshing package lists (" + mgr + ")…")
	if steps, err := syspkg.Plan(mgr, syspkg.ActRefresh, ""); err == nil {
		for _, step := range steps {
			if _, err := admin.Run(ctx, step...); err != nil {
				fmt.Fprintln(os.Stderr, "cap: refresh failed:", err)
			}
		}
	}

	ups, err := admin.Updates(ctx)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if len(ups) == 0 {
		fmt.Println("System is up to date.")
		return nil
	}

	fmt.Printf("%d update(s) available:\n\n", len(ups))
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tCURRENT\tAVAILABLE\tHELD")
	for _, u := range ups {
		held := ""
		if u.Held {
			held = "yes"
		}
		current := u.Current
		if current == "" {
			current = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", u.Name, current, u.Available, held)
	}
	w.Flush()
	fmt.Println("\nInstall these from NoCapOS's Packages app, or your system's usual upgrade command.")
	return nil
}

// cmdLogs shows nocapos.service's own journal — the systemd unit alfad runs
// as (see deploy/nocapos.service).
func cmdLogs(args []string) error {
	follow, lines := false, "200"
	for _, a := range args {
		switch a {
		case "-f", "--follow":
			follow = true
		default:
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				lines = strconv.Itoa(n)
			}
		}
	}
	jArgs := []string{"-u", "nocapos", "-n", lines}
	if follow {
		jArgs = append(jArgs, "-f")
	} else {
		jArgs = append(jArgs, "--no-pager")
	}
	cmd := exec.Command("journalctl", jArgs...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}
