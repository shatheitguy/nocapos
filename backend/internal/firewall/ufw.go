package firewall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GuardComment marks the rules NoCapOS adds so whoever is changing the
// firewall stays connected.
const GuardComment = "nocap keep-me-in"

// KeepWindow is how long a risky change waits for "Keep these changes"
// before it's undone on its own.
const KeepWindow = 60 * time.Second

var (
	// ErrUnsupported: this host can't run the firewall (not Linux, not root,
	// inside a container). Reason() says why.
	ErrUnsupported = errors.New("the firewall needs NoCapOS running on Linux as root")
	// ErrNotInstalled: ufw isn't installed yet.
	ErrNotInstalled = errors.New("ufw isn't installed yet")
	// ErrStale: the rule changed since it was shown; reload and try again.
	ErrStale = errors.New("that rule changed in the meantime: reload and try again")
	// ErrNoPending: there's no change waiting to be kept or undone.
	ErrNoPending = errors.New("there's no change waiting to be kept")
)

// Runner runs a program with arguments (never through a shell) and returns
// its combined output.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

func execRunner(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, errors.New(lastLine(s))
	}
	return s, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// Keep says who must stay connected through a change: the client's address
// and the TCP ports it needs (the NoCapOS web port and SSH).
type Keep struct {
	Client netip.Addr
	Ports  []int
}

// Pending is a risky change that's undone unless it's kept in time.
type Pending struct {
	ID       string    `json:"id"`
	What     string    `json:"what"`
	Deadline time.Time `json:"deadline"`
}

// State is everything the Shield app's firewall pages show.
type State struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	Installed bool   `json:"installed"`
	Report
	IPv6    bool         `json:"ipv6"`
	Rules   []Entry      `json:"rules"`
	Apps    []AppProfile `json:"apps"`
	Pending *Pending     `json:"pending,omitempty"`
}

// Engine drives ufw. One change runs at a time.
type Engine struct {
	run  Runner
	etc  string // "/etc"; a temporary folder in tests
	have func() bool

	mu      sync.Mutex
	pending *Pending
	snap    *snapshot
	timer   *time.Timer
	// OnRevert is told when a change was undone because nobody kept it.
	OnRevert func(what string, err error)
}

// New returns the engine for this machine.
func New() *Engine {
	return &Engine{run: execRunner, etc: "/etc", have: func() bool { _, err := exec.LookPath("ufw"); return err == nil }}
}

// NewWith is New with another runner and /etc (tests).
func NewWith(run Runner, etc string) *Engine {
	return &Engine{run: run, etc: etc, have: func() bool { return true }}
}

func (e *Engine) ready() error {
	if e.etc == "/etc" {
		if ok, _ := hostSupported(); !ok {
			return ErrUnsupported
		}
	}
	if !e.have() {
		return ErrNotInstalled
	}
	return nil
}

// Supported reports whether this host can run the firewall, and if not why.
func Supported() (bool, string) { return hostSupported() }

func (e *Engine) ufw(ctx context.Context, args ...string) (string, error) {
	out, err := e.run(ctx, "ufw", args...)
	if err != nil {
		return out, fmt.Errorf("ufw %s: %w", args[0], err)
	}
	return out, nil
}

// Status reads the whole firewall.
func (e *Engine) Status(ctx context.Context) (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status(ctx)
}

func (e *Engine) status(ctx context.Context) (State, error) {
	st := State{Rules: []Entry{}, Apps: []AppProfile{}}
	if e.etc == "/etc" {
		st.Supported, st.Reason = hostSupported()
		if !st.Supported {
			return st, nil
		}
	} else {
		st.Supported = true
	}
	st.Installed = e.have()
	if !st.Installed {
		return st, nil
	}
	st.Report = e.fileDefaults()
	st.IPv6 = e.ipv6()
	out, err := e.ufw(ctx, "status", "verbose")
	if err != nil {
		return st, err
	}
	live := ParseReport(out)
	st.Active = live.Active
	if live.Active {
		// The running firewall is the truth while it runs.
		if live.Incoming != "" {
			st.Incoming, st.Outgoing, st.Routed, st.Logging = live.Incoming, live.Outgoing, live.Routed, live.Logging
		}
		out, err = e.ufw(ctx, "status", "numbered")
		if err != nil {
			return st, err
		}
		_, st.Rules = ParseNumbered(out)
	} else {
		out, err = e.ufw(ctx, "show", "added")
		if err != nil {
			return st, err
		}
		st.Rules = ParseAdded(out)
	}
	if st.Rules == nil {
		st.Rules = []Entry{}
	}
	st.Apps = e.apps(ctx)
	if e.pending != nil {
		p := *e.pending
		st.Pending = &p
	}
	return st, nil
}

func (e *Engine) apps(ctx context.Context) []AppProfile {
	out, err := e.ufw(ctx, "app", "list")
	if err != nil {
		return []AppProfile{}
	}
	names := ParseAppList(out)
	known := map[string]AppProfile{}
	files, _ := filepath.Glob(filepath.Join(e.etc, "ufw", "applications.d", "*"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && len(b) < 1<<20 {
			for _, p := range ParseAppProfiles(string(b)) {
				known[p.Name] = p
			}
		}
	}
	list := make([]AppProfile, 0, len(names))
	for _, n := range names {
		p, ok := known[n]
		if !ok {
			p = AppProfile{Name: n}
		}
		list = append(list, p)
	}
	return list
}

func policyWord(v string) string {
	switch strings.ToUpper(strings.Trim(v, `"' `)) {
	case "ACCEPT":
		return "allow"
	case "REJECT":
		return "reject"
	case "DROP":
		return "deny"
	}
	return ""
}

// readKV reads a shell-style KEY=value file.
func readKV(path string) map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return m
}

// fileDefaults reads the defaults and logging level ufw keeps on disk (the
// only place they show while the firewall is off).
func (e *Engine) fileDefaults() Report {
	d := readKV(filepath.Join(e.etc, "default", "ufw"))
	c := readKV(filepath.Join(e.etc, "ufw", "ufw.conf"))
	r := Report{Incoming: policyWord(d["DEFAULT_INPUT_POLICY"]), Outgoing: policyWord(d["DEFAULT_OUTPUT_POLICY"]), Routed: policyWord(d["DEFAULT_FORWARD_POLICY"]), Logging: "off"}
	if r.Incoming == "" {
		r.Incoming = "deny"
	}
	if r.Outgoing == "" {
		r.Outgoing = "allow"
	}
	if lv := strings.ToLower(c["LOGLEVEL"]); lv != "" {
		r.Logging = lv
	}
	return r
}

func (e *Engine) ipv6() bool {
	v, ok := readKV(filepath.Join(e.etc, "default", "ufw"))["IPV6"]
	return !ok || strings.EqualFold(v, "yes")
}

// ---------- lock-out protection ----------

// Reachable predicts whether a new TCP connection from client to port gets
// through: ufw's first matching incoming rule decides, else the default.
// Whatever can't be known for sure counts against the connection when it
// would block it, and not for it when it would let it through.
func Reachable(defaultIn string, ipv6 bool, rules []Entry, client netip.Addr, port int) bool {
	client = client.Unmap()
	if client.IsValid() && client.IsLoopback() {
		return true // ufw always lets the machine reach itself
	}
	is6 := client.Is6()
	if is6 && !ipv6 {
		return true // ufw leaves IPv6 alone
	}
	for _, r := range rules {
		if r.Direction != "in" {
			continue
		}
		blocks := r.Action == "deny" || r.Action == "reject"
		if !appliesFamily(r, is6) {
			continue
		}
		if !r.Editable && r.Raw != "" {
			// A rule NoCapOS can't read in full.
			if blocks {
				return false
			}
			continue
		}
		if !r.CoversPort(port, "tcp") || !r.CoversSource(client) {
			continue
		}
		unsure := r.To != "" || r.Interface != "" || r.App != ""
		if unsure && !blocks {
			continue
		}
		return !blocks
	}
	return defaultIn == "allow"
}

func appliesFamily(r Entry, is6 bool) bool {
	for _, a := range []string{r.From, r.To} {
		if a != "" {
			return isV6(a) == is6
		}
	}
	if r.Number == 0 {
		return true // a rule as written: ufw adds it for both families
	}
	return r.V6 == is6
}

// lockedOut lists the ports in k that would be cut off.
func lockedOut(defaultIn string, ipv6 bool, rules []Entry, k Keep) []int {
	if !k.Client.IsValid() {
		return nil
	}
	var out []int
	for _, p := range k.Ports {
		if !Reachable(defaultIn, ipv6, rules, k.Client, p) {
			out = append(out, p)
		}
	}
	return out
}

func guardRule(client netip.Addr, port int) Rule {
	return Rule{Action: "allow", Direction: "in", Proto: "tcp", From: client.Unmap().String(), Port: strconv.Itoa(port), Comment: GuardComment}
}

// keepIn adds a guard rule at the top for each port in k that the planned
// rules and default would cut off. predicted is the rule list after the
// change, active whether the firewall will be running.
func (e *Engine) keepIn(ctx context.Context, st State, defaultIn string, predicted []Entry, active bool, k Keep) error {
	if !active {
		return nil
	}
	for _, p := range lockedOut(defaultIn, st.IPv6, predicted, k) {
		g := guardRule(k.Client, p)
		args := g.Args()
		if len(st.Rules) > 0 {
			args = append([]string{"insert", "1"}, args...)
		}
		if _, err := e.ufw(ctx, args...); err != nil {
			return fmt.Errorf("couldn't add the rule that keeps you connected: %w", err)
		}
		st.Rules = append([]Entry{{Rule: g, Editable: true, Guard: true}}, st.Rules...)
	}
	return nil
}

// ---------- changes ----------

type snapshot struct {
	active bool
	files  map[string][]byte // path → contents (nil: didn't exist)
}

func (e *Engine) ruleFiles() []string {
	return []string{
		filepath.Join(e.etc, "ufw", "user.rules"),
		filepath.Join(e.etc, "ufw", "user6.rules"),
		filepath.Join(e.etc, "ufw", "ufw.conf"),
		filepath.Join(e.etc, "default", "ufw"),
	}
}

func (e *Engine) takeSnapshot(active bool) *snapshot {
	s := &snapshot{active: active, files: map[string][]byte{}}
	for _, f := range e.ruleFiles() {
		b, err := os.ReadFile(f)
		if err != nil {
			b = nil
		}
		s.files[f] = b
	}
	return s
}

func (e *Engine) restore(ctx context.Context, s *snapshot) error {
	if !s.active {
		// Stop first: disabling rewrites ufw.conf, which is restored next.
		_, _ = e.ufw(ctx, "--force", "disable")
	}
	for f, b := range s.files {
		if b == nil {
			continue
		}
		if err := writeFileAtomic(f, b); err != nil {
			return err
		}
	}
	if s.active {
		_, err := e.ufw(ctx, "--force", "enable")
		if err == nil {
			_, err = e.ufw(ctx, "reload")
		}
		return err
	}
	return nil
}

func writeFileAtomic(path string, b []byte) error {
	mode := os.FileMode(0o640)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".nocap-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// risky starts (or extends) the keep-or-undo window before a change. The
// snapshot taken first is kept, so undoing returns to how things were
// before the first change nobody has kept yet.
func (e *Engine) risky(what string, active bool) {
	if e.snap == nil {
		e.snap = e.takeSnapshot(active)
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	e.pending = &Pending{ID: hex.EncodeToString(id), What: what, Deadline: time.Now().Add(KeepWindow)}
	if e.timer != nil {
		e.timer.Stop()
	}
	pid := e.pending.ID
	e.timer = time.AfterFunc(KeepWindow, func() { e.expire(pid) })
}

func (e *Engine) expire(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.ID != id {
		return
	}
	what := e.pending.What
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := e.restore(ctx, e.snap)
	e.clearPending()
	if e.OnRevert != nil {
		go e.OnRevert(what, err)
	}
}

func (e *Engine) clearPending() {
	if e.timer != nil {
		e.timer.Stop()
	}
	e.pending, e.snap, e.timer = nil, nil, nil
}

// KeepChanges confirms the change waiting under id.
func (e *Engine) KeepChanges(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.ID != id {
		return ErrNoPending
	}
	e.clearPending()
	return nil
}

// Undo puts back how things were before the change waiting under id.
func (e *Engine) Undo(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.ID != id {
		return ErrNoPending
	}
	err := e.restore(ctx, e.snap)
	e.clearPending()
	return err
}

// change runs fn with the current state under the lock; if risky (and the
// firewall is or will be running) the keep-or-undo window starts first.
func (e *Engine) change(ctx context.Context, what string, risky func(State) bool, fn func(State) error) (State, error) {
	if err := e.ready(); err != nil {
		return State{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.status(ctx)
	if err != nil {
		return st, err
	}
	if risky != nil && risky(st) {
		e.risky(what, st.Active)
	}
	if err := fn(st); err != nil {
		if s, err2 := e.status(ctx); err2 == nil {
			st = s
		}
		return st, err
	}
	return e.status(ctx)
}

// Enable turns the firewall on, first allowing k's ports from k's client
// if the rules wouldn't. It has to be kept within KeepWindow.
func (e *Engine) Enable(ctx context.Context, k Keep) (State, error) {
	return e.change(ctx, "enable", func(st State) bool { return !st.Active }, func(st State) error {
		if st.Active {
			return nil
		}
		// "show added" lists rules without numbers: the order they run in.
		if err := e.keepIn(ctx, st, st.Incoming, st.Rules, true, k); err != nil {
			return err
		}
		_, err := e.ufw(ctx, "--force", "enable")
		return err
	})
}

// Disable turns the firewall off (never risky).
func (e *Engine) Disable(ctx context.Context) (State, error) {
	return e.change(ctx, "disable", nil, func(st State) error {
		_, err := e.ufw(ctx, "disable")
		return err
	})
}

// SetDefault sets the default policy for "incoming" or "outgoing".
func (e *Engine) SetDefault(ctx context.Context, dir, policy string, k Keep) (State, error) {
	if dir != "incoming" && dir != "outgoing" {
		return State{}, invalid("direction must be incoming or outgoing")
	}
	if policy != "allow" && policy != "deny" && policy != "reject" {
		return State{}, invalid("policy must be allow, deny or reject")
	}
	return e.change(ctx, "default "+dir, func(st State) bool { return st.Active && policy != "allow" }, func(st State) error {
		if dir == "incoming" {
			if err := e.keepIn(ctx, st, policy, st.Rules, st.Active, k); err != nil {
				return err
			}
		}
		_, err := e.ufw(ctx, "default", policy, dir)
		return err
	})
}

// SetLogging sets ufw's logging level.
func (e *Engine) SetLogging(ctx context.Context, level string) (State, error) {
	switch level {
	case "off", "low", "medium", "high", "full":
	default:
		return State{}, invalid("logging must be off, low, medium, high or full")
	}
	return e.change(ctx, "logging", nil, func(State) error {
		_, err := e.ufw(ctx, "logging", level)
		return err
	})
}

// SetIPv6 turns ufw's IPv6 filtering on or off.
func (e *Engine) SetIPv6(ctx context.Context, on bool) (State, error) {
	return e.change(ctx, "ipv6", func(st State) bool { return st.Active }, func(st State) error {
		path := filepath.Join(e.etc, "default", "ufw")
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		val := "no"
		if on {
			val = "yes"
		}
		lines := strings.Split(string(b), "\n")
		found := false
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "IPV6=") {
				lines[i], found = "IPV6="+val, true
			}
		}
		if !found {
			lines = append(lines, "IPV6="+val)
		}
		if err := writeFileAtomic(path, []byte(strings.Join(lines, "\n"))); err != nil {
			return err
		}
		if st.Active {
			_, err = e.ufw(ctx, "reload")
		}
		return err
	})
}

// find returns the rule currently shown as raw.
func find(st State, raw string) (Entry, int, error) {
	for i, r := range st.Rules {
		if r.Raw == raw {
			return r, i, nil
		}
	}
	return Entry{}, -1, ErrStale
}

func withRule(rules []Entry, at int, r Rule) []Entry {
	out := make([]Entry, 0, len(rules)+1)
	out = append(out, rules[:at]...)
	out = append(out, Entry{Rule: r, Editable: true})
	return append(out, rules[at:]...)
}

func without(rules []Entry, at int) []Entry {
	out := make([]Entry, 0, len(rules))
	out = append(out, rules[:at]...)
	return append(out, rules[at+1:]...)
}

// position is where an inserted rule lands in ufw's numbering, or 0 to add
// at the end.
func position(st State, at int) int {
	if !st.Active || at >= len(st.Rules) {
		return 0
	}
	return st.Rules[at].Number
}

func (e *Engine) addAt(ctx context.Context, st State, at int, r Rule) error {
	args := r.Args()
	if pos := position(st, at); pos > 0 {
		args = append([]string{"insert", strconv.Itoa(pos)}, args...)
	}
	_, err := e.ufw(ctx, args...)
	return err
}

// deleteEntry removes one rule: by number while the firewall runs, else by
// the words that made it.
func (e *Engine) deleteEntry(ctx context.Context, st State, r Entry) error {
	if st.Active && r.Number > 0 {
		_, err := e.ufw(ctx, "--force", "delete", strconv.Itoa(r.Number))
		return err
	}
	spec := r.spec
	if len(spec) == 0 {
		if !r.Editable {
			return ErrStale
		}
		spec = r.Args()
	}
	_, err := e.ufw(ctx, append([]string{"--force", "delete"}, spec...)...)
	return err
}

// Add adds r at index at of the shown list (or at the end when at < 0).
func (e *Engine) Add(ctx context.Context, r Rule, at int, k Keep) (State, error) {
	r, err := Normalize(r)
	if err != nil {
		return State{}, err
	}
	blocks := r.Action != "allow" && r.Action != "limit"
	return e.change(ctx, "add rule", func(st State) bool { return st.Active && blocks }, func(st State) error {
		if at < 0 || at > len(st.Rules) {
			at = len(st.Rules)
		}
		if err := e.keepIn(ctx, st, st.Incoming, withRule(st.Rules, at, r), st.Active, k); err != nil {
			return err
		}
		if st.Active && blocks {
			// Guard rules went in at the top: the list moved down.
			if s, err := e.status(ctx); err == nil {
				at += len(s.Rules) - len(st.Rules)
				st = s
			}
		}
		return e.addAt(ctx, st, at, r)
	})
}

// Delete removes the rule shown as raw.
func (e *Engine) Delete(ctx context.Context, raw string, k Keep) (State, error) {
	return e.change(ctx, "delete rule", func(st State) bool { return st.Active }, func(st State) error {
		r, i, err := find(st, raw)
		if err != nil {
			return err
		}
		if err := e.keepIn(ctx, st, st.Incoming, without(st.Rules, i), st.Active, k); err != nil {
			return err
		}
		if s, err := e.status(ctx); err == nil {
			if r, _, err = find(s, raw); err != nil {
				return err
			}
			st = s
		}
		return e.deleteEntry(ctx, st, r)
	})
}

// Update replaces the rule shown as raw with r, in the same place.
func (e *Engine) Update(ctx context.Context, raw string, r Rule, k Keep) (State, error) {
	r, err := Normalize(r)
	if err != nil {
		return State{}, err
	}
	return e.change(ctx, "edit rule", func(st State) bool { return st.Active }, func(st State) error {
		return e.replace(ctx, st, raw, r, -1, k)
	})
}

// Move moves the rule shown as raw to index to of the shown list.
func (e *Engine) Move(ctx context.Context, raw string, to int, k Keep) (State, error) {
	return e.change(ctx, "move rule", func(st State) bool { return st.Active }, func(st State) error {
		old, _, err := find(st, raw)
		if err != nil {
			return err
		}
		if !old.Editable {
			return invalid("this rule can't be moved from here")
		}
		return e.replace(ctx, st, raw, old.Rule, to, k)
	})
}

// replace deletes the rule shown as raw and puts r at index to (its own
// place when to < 0). If adding fails, the old rule goes back.
func (e *Engine) replace(ctx context.Context, st State, raw string, r Rule, to int, k Keep) error {
	old, i, err := find(st, raw)
	if err != nil {
		return err
	}
	if to < 0 {
		to = i
	}
	if to >= len(st.Rules) {
		to = len(st.Rules) - 1
	}
	predicted := withRule(without(st.Rules, i), to, r)
	if err := e.keepIn(ctx, st, st.Incoming, predicted, st.Active, k); err != nil {
		return err
	}
	st, err = e.status(ctx)
	if err != nil {
		return err
	}
	if old, i, err = find(st, raw); err != nil {
		return err
	}
	if err := e.deleteEntry(ctx, st, old); err != nil {
		return err
	}
	after, err := e.status(ctx)
	if err != nil {
		return err
	}
	// ufw drops a rule's IPv6 twin along with it: find where "to" now is
	// by counting the rules of the same family.
	at := sameFamilyIndex(after, to, old.V6)
	if err := e.addAt(ctx, after, at, r); err != nil {
		if old.Editable {
			_ = e.addAt(ctx, after, sameFamilyIndex(after, i, old.V6), old.Rule)
		}
		return err
	}
	return nil
}

// sameFamilyIndex maps an index in the full list to the index ufw expects
// for inserting a rule of the given family: IPv6 rules are numbered after
// all IPv4 rules, so an IPv4 rule can't be inserted among them.
func sameFamilyIndex(st State, to int, v6 bool) int {
	if !st.Active {
		return to
	}
	if to > len(st.Rules) {
		to = len(st.Rules)
	}
	if !v6 {
		// Clamp into the IPv4 block.
		n4 := 0
		for _, r := range st.Rules {
			if !r.V6 {
				n4++
			}
		}
		if to > n4 {
			return n4
		}
	}
	return to
}

// Exposed is one thing listening for connections from other devices.
type Exposed struct {
	Port    int    `json:"port"`
	Proto   string `json:"proto"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // nocap | ssh | container | system
	Process string `json:"process,omitempty"`
	// Open: other devices can reach it through the firewall right now (or
	// would, once it's on).
	Open bool `json:"open"`
	// Locked: switching it off would cut off the person looking.
	Locked bool `json:"locked,omitempty"`
	// Bypass: container ports are published by the container engine, which
	// writes its own packet-filter rules before ufw's.
	Bypass bool `json:"bypass,omitempty"`
}

// ReachableFromAnyone is Reachable for any outside address (no source rule).
func ReachableFromAnyone(st State, port int, proto string) bool {
	for _, r := range st.Rules {
		if r.Direction != "in" || (r.V6 && st.Active) {
			continue
		}
		blocks := r.Action == "deny" || r.Action == "reject"
		if !r.Editable {
			if blocks {
				return false
			}
			continue
		}
		if !r.CoversPort(port, proto) {
			continue
		}
		if r.From != "" || r.To != "" || r.Interface != "" || r.App != "" {
			continue // only some sources or addresses: not "anyone"
		}
		return !blocks
	}
	return st.Incoming == "allow"
}

// SetExposed opens or closes one port to everyone.
func (e *Engine) SetExposed(ctx context.Context, port int, proto, name string, open bool, k Keep) (State, error) {
	if port < 1 || port > 65535 || (proto != "tcp" && proto != "udp") {
		return State{}, invalid("not a port")
	}
	return e.change(ctx, "service", func(st State) bool { return st.Active && !open }, func(st State) error {
		if open {
			comment := sanitizeComment(name)
			r := Rule{Action: "allow", Direction: "in", Proto: proto, Port: strconv.Itoa(port), Comment: comment}
			n, err := Normalize(r)
			if err != nil {
				return err
			}
			// In front of anything that blocks it.
			return e.addAt(ctx, st, 0, n)
		}
		// Remove the plain "allow this port" rules (both families go together).
		var keepPredicted []Entry
		var drop []string
		for _, r := range st.Rules {
			if r.Editable && r.Action != "deny" && r.Action != "reject" && r.OnlyPort(port, proto) && !r.Guard {
				if !r.V6 || !st.Active {
					drop = append(drop, r.Raw)
				}
				continue
			}
			keepPredicted = append(keepPredicted, r)
		}
		if err := e.keepIn(ctx, st, st.Incoming, keepPredicted, st.Active, k); err != nil {
			return err
		}
		for _, raw := range drop {
			cur, err := e.status(ctx)
			if err != nil {
				return err
			}
			r, _, err := find(cur, raw)
			if err != nil {
				continue // already gone with its twin
			}
			if err := e.deleteEntry(ctx, cur, r); err != nil {
				return err
			}
		}
		cur, err := e.status(ctx)
		if err != nil {
			return err
		}
		if ReachableFromAnyone(cur, port, proto) {
			// The default lets it in: say no explicitly, after the guards.
			r, err := Normalize(Rule{Action: "deny", Direction: "in", Proto: proto, Port: strconv.Itoa(port), Comment: sanitizeComment(name)})
			if err != nil {
				return err
			}
			return e.addAt(ctx, cur, len(cur.Rules), r)
		}
		return nil
	})
}

func sanitizeComment(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(`'"\#`, c) {
			continue
		}
		b.WriteRune(c)
	}
	out := strings.TrimSpace(b.String())
	if len(out) > MaxComment {
		out = out[:MaxComment]
	}
	return out
}

// Preset is a one-click set of rules for a common service.
type Preset struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Ports []string `json:"ports"` // "22/tcp", "137:138/udp"
}

// Presets are the common services the Shield app offers in one click.
var Presets = []Preset{
	{ID: "ssh", Name: "SSH", Ports: []string{"22/tcp"}},
	{ID: "web", Name: "Web (HTTP and HTTPS)", Ports: []string{"80/tcp", "443/tcp"}},
	{ID: "smb", Name: "File sharing (SMB)", Ports: []string{"139/tcp", "445/tcp", "137:138/udp"}},
	{ID: "nfs", Name: "Network drives (NFS)", Ports: []string{"2049/tcp", "2049/udp"}},
	{ID: "dns", Name: "DNS", Ports: []string{"53/tcp", "53/udp"}},
	{ID: "mdns", Name: "Local names (mDNS)", Ports: []string{"5353/udp"}},
	{ID: "discovery", Name: "Network discovery (WS-Discovery)", Ports: []string{"3702/udp", "5357/tcp"}},
	{ID: "rdp", Name: "Remote Desktop (RDP)", Ports: []string{"3389/tcp"}},
	{ID: "vnc", Name: "VNC", Ports: []string{"5900/tcp"}},
	{ID: "ntp", Name: "Time (NTP)", Ports: []string{"123/udp"}},
}

// PresetRules turns a preset into rules, from source ("" = anywhere).
func PresetRules(id, source string) ([]Rule, error) {
	for _, p := range Presets {
		if p.ID != id {
			continue
		}
		var out []Rule
		for _, pp := range p.Ports {
			port, proto, _ := strings.Cut(pp, "/")
			r, err := Normalize(Rule{Action: "allow", Direction: "in", Proto: proto, Port: port, From: source, Comment: sanitizeComment(p.Name)})
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	}
	return nil, invalid("no such preset")
}

// AddMany adds several allow rules (a preset) in one change.
func (e *Engine) AddMany(ctx context.Context, rules []Rule) (State, error) {
	return e.change(ctx, "preset", nil, func(st State) error {
		for _, r := range rules {
			if _, err := e.ufw(ctx, r.Args()...); err != nil {
				return err
			}
		}
		return nil
	})
}

// sortedUnique returns ports sorted without repeats.
func sortedUnique(ps []int) []int {
	sort.Ints(ps)
	out := ps[:0]
	for i, p := range ps {
		if i == 0 || p != ps[i-1] {
			out = append(out, p)
		}
	}
	return out
}

// KeepFor builds a Keep for client and the given TCP ports.
func KeepFor(client string, ports ...int) Keep {
	a, _ := netip.ParseAddr(client)
	var ps []int
	for _, p := range ports {
		if p > 0 && p <= 65535 {
			ps = append(ps, p)
		}
	}
	return Keep{Client: a, Ports: sortedUnique(ps)}
}
