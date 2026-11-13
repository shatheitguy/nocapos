package mcp

import (
	"errors"
	"slices"
)

// Code execution for agents: a snippet in one of a few languages, run
// either in a throwaway sandbox (no network, no home folders, read-only
// system, its own temporary user) or on the host as the owner's own
// account (never root).

// CodeLanguages are the languages run_code takes.
var CodeLanguages = []string{"python", "node", "sh"}

// MaxCode is the longest snippet run_code takes.
const MaxCode = 64 << 10

var (
	// ErrSandboxUnavailable: this server can't build the sandbox.
	ErrSandboxUnavailable = errors.New("the code sandbox needs NoCapOS running as a system service on Linux with systemd")
	// ErrBadCode: an empty, too long or unknown-language snippet.
	ErrBadCode = errors.New("code must be 1 character to 64 KB, in python, node or sh")
)

// codeArgv is the command line that runs code in lang (interpreter
// looked up by look; nil when it isn't installed).
func codeArgv(lang, code string, look func(string) (string, error)) ([]string, error) {
	if code == "" || len(code) > MaxCode || !slices.Contains(CodeLanguages, lang) {
		return nil, ErrBadCode
	}
	bin, flag := "/bin/sh", "-c"
	switch lang {
	case "python":
		bin = "python3"
	case "node":
		bin, flag = "node", "-e"
	}
	p, err := look(bin)
	if err != nil {
		return nil, errors.New(lang + " isn't installed on this server")
	}
	return []string{p, flag, code}, nil
}
