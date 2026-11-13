package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"alfaos/alfad/internal/hostaccess"
)

type fakeDesk struct {
	health   hostaccess.DesktopHealth
	repaired bool
	reset    bool
}

func (f *fakeDesk) Health(context.Context) hostaccess.DesktopHealth { return f.health }

func (f *fakeDesk) Repair(context.Context) (hostaccess.RepairResult, error) {
	f.repaired = true
	return hostaccess.RepairResult{Actions: []string{"sign-in screen starts at boot", "restarted gdm3.service"}, Health: hostaccess.DesktopHealth{Status: "starting"}}, nil
}

func (f *fakeDesk) Diagnostics(context.Context) string { return "===== NoCap Desktop health =====\n" }

func (f *fakeDesk) ResetFallback(context.Context) error {
	f.reset = true
	return nil
}

func TestCmdDesktop(t *testing.T) {
	ctx := context.Background()
	d := &fakeDesk{health: hostaccess.DesktopHealth{Status: "broken", Fallback: "xorg", Problems: []hostaccess.HealthProblem{{Code: "no_shell", Detail: "gpu"}}}}
	var out bytes.Buffer
	if err := cmdDesktop(ctx, &out, d, false, []string{"repair"}); err == nil || !strings.Contains(err.Error(), "sudo cap desktop repair") {
		t.Fatalf("non-root = %v", err)
	}
	if d.repaired {
		t.Fatal("repaired without root")
	}
	if err := cmdDesktop(ctx, &out, d, true, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NoCap Desktop: broken", "fallback in use: xorg", "no_shell: gpu", "sudo cap desktop repair"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := cmdDesktop(ctx, &out, d, true, []string{"repair"}); err != nil || !d.repaired || !strings.Contains(out.String(), "done: restarted gdm3.service") {
		t.Fatalf("repair = %v %s", err, out.String())
	}
	out.Reset()
	if err := cmdDesktop(ctx, &out, d, true, []string{"diagnose"}); err != nil || !strings.HasPrefix(out.String(), "===== NoCap Desktop health") {
		t.Fatalf("diagnose = %v %s", err, out.String())
	}
	if err := cmdDesktop(ctx, &out, d, true, []string{"reset-fallback"}); err != nil || !d.reset {
		t.Fatal("reset-fallback")
	}
	if err := cmdDesktop(ctx, &out, d, true, []string{"party"}); err == nil {
		t.Fatal("unknown option")
	}
	if err := cmdDesktop(ctx, &out, d, true, nil); err == nil {
		t.Fatal("no option")
	}
}
