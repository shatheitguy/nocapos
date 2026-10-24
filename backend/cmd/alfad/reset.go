package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/store"
)

// envFile is where the installer keeps NoCapOS's settings (ALFA_DATA_DIR…).
const envFile = "/etc/nocapos/nocapos.env"

// resetPassword is the local recovery for a locked-out administrator:
//
//	sudo nocapos reset-password <user>
//
// It works only as root on the machine itself (never over the network): it
// sets a new random password, turns 2FA off and ends the person's sessions.
func resetPassword(args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: nocapos reset-password <user>")
	}
	username := args[0]
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("run this as root on the NoCapOS machine: sudo nocapos reset-password " + username)
	}
	loadEnvFile(envFile)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	svc := auth.NewService(st, nil, auth.Options{})
	if cfg.AuthMode != "app" {
		if sys, err := accounts.NewSystem(); err == nil {
			svc.SetSystemAccounts(sys)
		} else if cfg.AuthMode == "system" {
			return fmt.Errorf("ALFA_AUTH=system: %w", err)
		}
	}
	next := randomPassword()
	if err := svc.ResetLocal(ctx, username, next); err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, auth.ErrNoAccount) {
			return fmt.Errorf("no NoCapOS user named %q", username)
		}
		return err
	}
	_ = st.Audit(ctx, store.AuditEntry{Action: "user.password.local_reset", Target: username, IP: "local", Success: true, Detail: "root on the machine; 2FA turned off"})
	fmt.Printf("Password for %s reset. Two-factor authentication is off and their sessions have ended.\n", username)
	fmt.Printf("Temporary password: %s\n", next)
	fmt.Println("Sign in with it, then choose a new one in Settings → Privacy & Security.")
	fmt.Println("If the account was locked after failed attempts, restart NoCapOS (systemctl restart nocapos) or wait 15 minutes.")
	return nil
}

// loadEnvFile applies KEY=VALUE lines from the installer's settings file
// for variables that aren't already set, so the command finds the same data
// directory as the service.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !strings.HasPrefix(k, "ALFA_") {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			_ = os.Setenv(k, v)
		}
	}
}

// randomPassword: 26 random base32 characters (130 bits).
func randomPassword() string { return rand.Text() }
