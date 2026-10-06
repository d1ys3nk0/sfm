package sfm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var Version = "dev"
var Commit = "unknown"

const usage = `SFM — Synced File Manager
Usage: sfm [--config FILE] COMMAND [OPTIONS]
Commands:
  snapshot [--dry] [--diff]   Copy selected files into the vault
  install [--dry] [--diff] [--force]
                            Create missing files; ask before replacing differences
  verify                    Check vault structure and supported types
  track PATH                Track and capture a file or directory
  forget PATH               Stop tracking a file or directory
Options: --dry previews without writing; --diff adds content differences
         --force installs without asking; cannot combine --dry and --force
Diff colors (with --diff): auto (default), always, never; --color means always
Global options: --config FILE, --help, --version
`

func Run(args []string, out, errOut io.Writer) int {
	code, e := runWithInput(args, os.Stdin, out)
	if e != nil {
		fmt.Fprintln(errOut, "sfm: "+e.Error())
		return 2
	}
	return code
}
func runWithInput(args []string, in io.Reader, out io.Writer) (int, error) {
	config := ""
	color := "auto"
	colorSet := false
	var positional []string
	dry, force, showDiff := false, false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--help", "-h":
			fmt.Fprint(out, usage)
			return 0, nil
		case "--version":
			fmt.Fprintf(out, "sfm %s (%s)\n", Version, Commit)
			return 0, nil
		case "--config":
			i++
			if i >= len(args) {
				return 2, fmt.Errorf("--config requires a path")
			}
			config = args[i]
		case "--dry":
			dry = true
		case "--force":
			force = true
		case "--color":
			color, colorSet = "always", true
		case "--diff":
			showDiff = true
		default:
			if strings.HasPrefix(a, "--config=") {
				config = strings.TrimPrefix(a, "--config=")
			} else if strings.HasPrefix(a, "--color=") {
				color, colorSet = strings.TrimPrefix(a, "--color="), true
			} else if strings.HasPrefix(a, "-") {
				return 2, fmt.Errorf("unknown option: %s", a)
			} else {
				positional = append(positional, a)
			}
		}
	}
	if len(positional) == 0 {
		fmt.Fprint(out, usage)
		return 0, nil
	}
	cmd := positional[0]
	if cmd != "snapshot" && cmd != "install" && cmd != "verify" && cmd != "track" && cmd != "forget" {
		return 2, fmt.Errorf("unknown command: %s", cmd)
	}
	want := 1
	if cmd == "track" || cmd == "forget" {
		want = 2
	}
	if len(positional) != want {
		return 2, fmt.Errorf("invalid arguments for %s", cmd)
	}
	if colorSet && !showDiff {
		return 2, fmt.Errorf("--color requires --diff")
	}
	if color != "auto" && color != "always" && color != "never" {
		return 2, fmt.Errorf("invalid color mode: %s", color)
	}
	if force && cmd != "install" || dry && cmd != "snapshot" && cmd != "install" || showDiff && cmd != "snapshot" && cmd != "install" || dry && force {
		return 2, fmt.Errorf("options are not valid for %s", cmd)
	}
	if config == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			h, e := os.UserHomeDir()
			if e != nil {
				return 2, e
			}
			base = filepath.Join(h, ".config")
		}
		config = filepath.Join(base, "sfm/config.toml")
	}
	c, e := readConfig(config)
	if e != nil {
		return 2, e
	}
	if cmd == "verify" {
		return (&manager{c: c, out: out}).inspect()
	}
	state, e := StateDir(c.Vault)
	if e != nil {
		return 2, e
	}
	if !dry && (cmd == "snapshot" || cmd == "install" || cmd == "track" || cmd == "forget") {
		unlock, e := lock(state)
		if e != nil {
			return 2, e
		}
		defer unlock()
	}
	m, e := newManager(c, out)
	if e != nil {
		return 2, e
	}
	m.color = color == "always" || color == "auto" && terminalOutput(out)
	switch cmd {
	case "snapshot":
		e = m.snapshot(dry, showDiff, "", nil)
	case "install":
		e = m.install(force, dry, showDiff, in)
	case "track", "forget":
		e = m.track(positional[1], cmd == "track")
	}
	return 0, e
}
func (m *manager) track(value string, adding bool) error {
	p, e := m.c.targetPath(value)
	if e != nil {
		return e
	}
	name := m.c.name(p)
	selectedBefore := map[string]Entry{}
	if !adding {
		v, e := m.vaultEntries()
		if e != nil {
			return e
		}
		if e = m.reconcile(v, name); e != nil {
			return e
		}
		selectedBefore = m.selected(v)
	}
	dir := false
	if exists(p) {
		d, e := entry(p)
		if e != nil {
			return e
		}
		dir = d.Type == "dir"
	} else if adding {
		return fmt.Errorf("target does not exist: %s", p)
	} else {
		v, e := m.vaultEntries()
		if e != nil {
			return e
		}
		if saved, ok := v[name]; ok {
			dir = saved.Type == "dir"
		} else if saved, ok := m.base.Entries[name]; ok {
			dir = saved.Type == "dir"
		}
	}

	relative := strings.SplitN(name, "/", 2)[1]
	var escaped strings.Builder
	for _, ch := range relative {
		if strings.ContainsRune(`\*?[]#! `, ch) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(ch)
	}
	prefix := "~/"
	if strings.HasPrefix(name, "root/") {
		prefix = "/"
	}
	pattern := prefix + escaped.String()
	if dir {
		pattern += "/"
	}
	if !adding {
		pattern = "!" + pattern
	}
	raw, e := editPolicy(m.c, name, dir, adding, pattern)
	if e != nil {
		return e
	}
	next := &Config{Vault: m.c.Vault, home: m.c.home}
	if e := decodePolicy(raw, next); e != nil {
		return e
	}
	m.c.rules = next.rules
	configD := bytesEntry(raw)
	if s, e := os.Stat(m.c.source); e == nil {
		configD.Mode = posixMode(s.Mode())
	}
	configAction := action{m.c.source, &configD, raw}
	if adding {
		return m.snapshot(false, false, name, []action{configAction})
	}
	v, e := m.vaultEntries()
	if e != nil {
		return e
	}
	if e = m.reconcile(v, name); e != nil {
		return e
	}
	removed := map[string]Entry{}
	for n, d := range v {
		if _, selected := selectedBefore[n]; selected && within(n, name) {
			removed[n] = d
		}
	}
	// Excluded payloads remain; their containing directories must remain too.
	for n, d := range removed {
		if d.Type != "dir" {
			continue
		}
		for child := range v {
			if child != n && within(child, n) {
				if _, gone := removed[child]; !gone {
					delete(removed, n)
					break
				}
			}
		}
	}
	actions := []action{configAction}
	for _, n := range sorted(removed, true) {
		actions = append(actions, action{path: m.c.vaultPath(n)})
		fmt.Fprintln(m.out, "delete "+strings.TrimPrefix(m.c.vaultPath(n), m.c.Vault+"/"))
	}
	baseEntries := clone(m.base.Entries)
	for n := range removed {
		delete(baseEntries, n)
	}
	bb := encoded(Record{Version: 3, Entries: baseEntries})
	bd := bytesEntry(bb)
	actions = append(actions, action{filepath.Join(m.state, "baseline.json"), &bd, bb})
	return apply(actions)
}

func (c *Config) targetPath(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("target path is required")
	}
	p, e := filepath.Abs(expand(value, c.home))
	if e != nil {
		return "", e
	}
	if p == c.home || p == "/" || within(p, c.Vault) || within(c.Vault, p) {
		return "", fmt.Errorf("unsafe target: %s", p)
	}
	if strings.ContainsAny(p, "\r\n") {
		return "", fmt.Errorf("target contains newline")
	}
	if _, e = c.destination(c.name(p)); e != nil {
		return "", e
	}
	if e = safe(p); e != nil {
		return "", e
	}
	return p, nil
}
