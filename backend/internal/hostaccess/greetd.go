package hostaccess

import (
	"errors"
	"os"
	"strings"
)

// ---- greetd: the sign-in service ----
//
// greetd shows NoCap's own sign-in screen (nocap-greeter-session, as its
// unprivileged account) and, when automatic sign-in is on, opens one
// person's desktop once per boot without it ([initial_session]).

// GreetdConfig is lib.sh's render_greetd_config: on the first console (vt
// 1), where the boot splash hands over to it.
func GreetdConfig(greeterUser, autologin string) string {
	s := "# NoCap Desktop's sign-in service (written by NoCapOS; Settings changes it).\n[terminal]\nvt = 1\n\n[default_session]\ncommand = \"" +
		greeterCmd + "\"\nuser = \"" + greeterUser + "\"\n"
	if autologin != "" {
		s += "\n[initial_session]\ncommand = \"" + sessionCmd + "\"\nuser = \"" + autologin + "\"\n"
	}
	return s
}

// GreetdAutologin is the account a greetd config signs in automatically
// ("" when none): lib.sh's greetd_autologin.
func GreetdAutologin(conf string) string {
	sec := ""
	for _, line := range strings.Split(conf, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") {
			sec = s
			continue
		}
		if sec != "[initial_session]" {
			continue
		}
		k, v, ok := strings.Cut(s, "=")
		if ok && strings.TrimSpace(k) == "user" {
			v = strings.Trim(strings.TrimSpace(v), `"`)
			if userNameRe.MatchString(v) && len(v) <= 32 {
				return v
			}
			return ""
		}
	}
	return ""
}

// GreetdGreeterUser is the account a greetd config runs the sign-in
// screen as ("" when it doesn't say).
func GreetdGreeterUser(conf string) string {
	sec := ""
	for _, line := range strings.Split(conf, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") {
			sec = s
			continue
		}
		if k, v, ok := strings.Cut(s, "="); ok && sec == "[default_session]" && strings.TrimSpace(k) == "user" {
			v = strings.Trim(strings.TrimSpace(v), `"`)
			if greeterNameRe.MatchString(v) && len(v) <= 32 {
				return v
			}
		}
	}
	return ""
}

// greeterUser: greetd's own account on this machine.
func (d *Desktop) greeterUser() string {
	if b, err := d.readFile(d.greetdConf()); err == nil {
		if u := GreetdGreeterUser(string(b)); u != "" {
			return u
		}
	}
	if b, err := d.readFile("/etc/passwd"); err == nil {
		for _, u := range []string{"_greetd", "greetd", "greeter"} {
			if strings.Contains("\n"+string(b), "\n"+u+":") {
				return u
			}
		}
	}
	return "_greetd"
}

// readGreetd is the greetd config's text ("" when there's none yet).
func (d *Desktop) readGreetd() (string, error) {
	b, err := d.readFile(d.greetdConf())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return string(b), nil
}
