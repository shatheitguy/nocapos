package mcp

import (
	"errors"
	"slices"
	"testing"
)

func TestScopesDenyByDefault(t *testing.T) {
	var none Scopes
	for _, c := range Capabilities {
		for _, l := range c.Levels {
			if none.Allows(c.ID, l.ID) {
				t.Fatalf("empty profile allows %s:%s", c.ID, l.ID)
			}
		}
		if none.Level(c.ID) != Off {
			t.Fatalf("%s isn't off", c.ID)
		}
	}
	if none.AnyOn() || none.Risk() != RiskNone {
		t.Fatalf("empty profile: on=%v risk=%s", none.AnyOn(), none.Risk())
	}
	s := Scopes{Levels: map[string]string{CapFiles: Read, CapCode: Sandbox, "bogus": Write, CapLogs: "root"}}
	cases := []struct {
		capID, need string
		want        bool
	}{
		{CapFiles, Read, true}, {CapFiles, Write, false}, {CapFiles, Off, false}, {CapFiles, "", false},
		{CapCode, Sandbox, true}, {CapCode, Host, false},
		{CapLogs, Read, false}, // unknown level counts as off
		{"bogus", Write, false}, {CapTerminal, On, false}, {CapFiles, "admin", false},
	}
	for _, c := range cases {
		if got := s.Allows(c.capID, c.need); got != c.want {
			t.Fatalf("Allows(%s, %s) = %v", c.capID, c.need, got)
		}
	}
}

func TestScopesNormalize(t *testing.T) {
	for _, bad := range []Scopes{
		{Levels: map[string]string{"root": On}},
		{Levels: map[string]string{CapFiles: "everything"}},
		{Levels: map[string]string{CapFiles: Read}, Folders: []string{"no-colon"}},
		{Levels: map[string]string{CapFiles: Read}, Folders: []string{"a/b:/x"}},
		{Levels: map[string]string{CapFiles: Read}, Folders: []string{":/x"}},
		{Auto: []string{"nope"}},
	} {
		if _, err := bad.Normalize(); !errors.Is(err, ErrBadScopes) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	n, err := Scopes{
		Levels:  map[string]string{CapFiles: Write, CapLogs: Off, CapPower: On, CapApps: View},
		Folders: []string{"data:/b/../a//c/", `data:\x\y`, "data:/a/c"},
		// power never runs on its own, logs is off, apps view doesn't act... it still
		// may be listed (Acts), terminal is off: only files and apps stay.
		Auto: []string{CapPower, CapLogs, CapTerminal, CapFiles, CapFiles, CapApps},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n.Levels[CapLogs]; ok {
		t.Fatalf("off kept: %v", n.Levels)
	}
	if !slices.Equal(n.Folders, []string{"data:/a/c", "data:/x/y"}) {
		t.Fatalf("folders: %v", n.Folders)
	}
	if !slices.Equal(n.Auto, []string{CapApps, CapFiles}) {
		t.Fatalf("auto: %v", n.Auto)
	}
	if !n.Asks(CapPower) || n.Asks(CapFiles) || !n.Asks(CapTerminal) || !n.Asks("bogus") {
		t.Fatal("asks")
	}
	// Folders past the limit.
	many := Scopes{}
	for i := 0; i < 51; i++ {
		many.Folders = append(many.Folders, "data:/"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if _, err := many.Normalize(); !errors.Is(err, ErrBadScopes) {
		t.Fatalf("51 folders: %v", err)
	}
}

func TestScopesFolders(t *testing.T) {
	s := Scopes{Folders: []string{"data:/Documents", "home-alex:/"}}
	cases := []struct {
		root, p string
		want    bool
	}{
		{"data", "/Documents", true}, {"data", "/Documents/a/b.txt", true}, {"data", "Documents/x", true},
		{"data", "/DocumentsX/y", false}, {"data", "/Documents/../secret", false}, {"data", `\Documents\..\secret`, false},
		{"data", "/", false}, {"data", "", false}, {"other", "/Documents", false}, {"", "/Documents", false},
		{"home-alex", "/anything/at/all", true}, {"data", "/Documents/\x00", false},
	}
	for _, c := range cases {
		if got := s.FolderAllows(c.root, c.p); got != c.want {
			t.Fatalf("FolderAllows(%q, %q) = %v", c.root, c.p, got)
		}
	}
	if !slices.Equal(s.FolderRoots(), []string{"data", "home-alex"}) {
		t.Fatalf("roots: %v", s.FolderRoots())
	}
	if !(Scopes{}).FolderAllows("x", "/y") || (Scopes{}).FolderRoots() != nil {
		t.Fatal("no folders should mean no limit")
	}
}

func TestScopesRisk(t *testing.T) {
	read, _ := PresetScopes("read_only")
	if read.Risk() != RiskLow {
		t.Fatalf("read_only risk %s", read.Risk())
	}
	files := Scopes{Levels: map[string]string{CapFiles: Write}}
	if files.Risk() != RiskMedium {
		t.Fatalf("files write %s", files.Risk())
	}
	files.Auto = []string{CapFiles} // medium without asking counts as high
	if files.Risk() != RiskHigh {
		t.Fatalf("files write auto %s", files.Risk())
	}
	if LevelRisk(CapTerminal, On) != RiskHigh || LevelRisk("x", On) != RiskNone {
		t.Fatal("level risk")
	}
}

func TestPresetsAndLegacy(t *testing.T) {
	for _, p := range Presets {
		n, err := p.Scopes.Normalize()
		if err != nil {
			t.Fatalf("preset %s: %v", p.ID, err)
		}
		if _, ok := PresetScopes(p.ID); !ok {
			t.Fatalf("preset %s", p.ID)
		}
		if slices.Contains(n.Auto, CapPower) {
			t.Fatalf("preset %s runs power without asking", p.ID)
		}
	}
	if off, _ := PresetScopes("off"); off.AnyOn() {
		t.Fatal("off preset allows something")
	}
	if _, ok := PresetScopes("nope"); ok {
		t.Fatal("unknown preset")
	}
	read := LegacyScopes("read")
	for _, c := range Capabilities {
		if c.Acts && len(c.Levels) > 2 && read.Allows(c.ID, c.Levels[2].ID) {
			t.Fatalf("legacy read allows %s:%s", c.ID, c.Levels[2].ID)
		}
	}
	if read.Allows(CapTerminal, On) || len(read.Auto) != 0 {
		t.Fatal("legacy read runs commands")
	}
	act := LegacyScopes("act")
	if !act.Allows(CapFiles, Write) || !act.Allows(CapTerminal, On) || act.Allows(CapCode, Sandbox) || act.Allows(CapPower, On) || len(act.Auto) != 0 {
		t.Fatalf("legacy act: %+v", act)
	}
	if LegacyScopes("custom").AnyOn() || LegacyScopes("").AnyOn() {
		t.Fatal("unknown legacy scope allows something")
	}
}

func TestAllowList(t *testing.T) {
	list, err := ParseAllowList([]string{" 10.0.0.0/8 ", "192.168.1.5", "", "::ffff:192.168.1.5", "10.1.2.3/8"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list, []string{"10.0.0.0/8", "192.168.1.5"}) {
		t.Fatalf("list: %v", list)
	}
	for _, bad := range []string{"10.0.0.0/33", "example.com", "1.2.3"} {
		if _, err := ParseAllowList([]string{bad}); !errors.Is(err, ErrBadScopes) {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	for ip, want := range map[string]bool{"10.9.9.9": true, "192.168.1.5": true, "::ffff:192.168.1.5": true, "192.168.1.6": false, "junk": false} {
		if AllowListed(list, ip) != want {
			t.Fatalf("AllowListed(%s)", ip)
		}
	}
	if !AllowListed(nil, "8.8.8.8") {
		t.Fatal("empty list should allow all")
	}
}

func TestCodeArgv(t *testing.T) {
	look := func(b string) (string, error) {
		if b == "node" {
			return "", errors.New("missing")
		}
		return "/usr/bin/" + b, nil
	}
	if a, err := codeArgv("python", "print(1)", look); err != nil || !slices.Equal(a, []string{"/usr/bin/python3", "-c", "print(1)"}) {
		t.Fatalf("python: %v %v", a, err)
	}
	if _, err := codeArgv("node", "1", look); err == nil {
		t.Fatal("node isn't installed")
	}
	for _, c := range [][2]string{{"ruby", "x"}, {"sh", ""}, {"sh", string(make([]byte, MaxCode+1))}} {
		if _, err := codeArgv(c[0], c[1], look); !errors.Is(err, ErrBadCode) {
			t.Fatalf("%s: %v", c[0], err)
		}
	}
}
