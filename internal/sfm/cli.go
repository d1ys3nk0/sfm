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
  snapshot [--dry] [--json]   Copy selected files into the vault
  install [--dry] [--force]   Install missing files; force replaces differences
  diff [PATH]               Compare all files or one file/directory subtree
  verify                    Check selection and metadata integrity
  track PATH                Track and capture a file or directory
  forget PATH               Stop tracking a file or directory
Global options: --config FILE, --help, --version
`

func Run(args []string, out, errOut io.Writer) int {
	code, e := run(args, out)
	if e != nil {
		fmt.Fprintln(errOut, "sfm: "+e.Error())
		return 2
	}
	return code
}
func run(args []string, out io.Writer) (int, error) {
	config := ""
	var positional []string
	dry, force, jsonOutput := false, false, false
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
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(a, "--config=") {
				config = strings.TrimPrefix(a, "--config=")
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
	if cmd != "snapshot" && cmd != "install" && cmd != "diff" && cmd != "verify" && cmd != "track" && cmd != "forget" {
		return 2, fmt.Errorf("unknown command: %s", cmd)
	}
	want := 1
	if cmd == "track" || cmd == "forget" {
		want = 2
	}
	if len(positional) != want && !(cmd == "diff" && len(positional) == 2) {
		return 2, fmt.Errorf("invalid arguments for %s", cmd)
	}
	if force && cmd != "install" || dry && cmd != "snapshot" && cmd != "install" || jsonOutput && (cmd != "snapshot" || !dry) {
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
	switch cmd {
	case "snapshot":
		e = m.snapshot(dry, jsonOutput, "", nil)
	case "install":
		e = m.install(force, dry)
	case "diff":
		scope := ""
		if len(positional) == 2 {
			path, e := c.targetPath(positional[1])
			if e != nil {
				return 2, e
			}
			scope = c.name(path)
		}
		return m.inspect(true, scope)
	case "verify":
		return m.inspect(false, "")
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
	if !adding {
		v, e := m.vaultEntries()
		if e != nil {
			return e
		}
		if e = m.reconcile(v, name); e != nil {
			return e
		}
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
	} else if saved, ok := m.meta.Entries[name]; ok {
		dir = saved.Type == "dir"
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
	meta := clone(m.meta.Entries)
	future := clone(v)
	for n, d := range v {
		if within(n, name) {
			removed[n] = d
			delete(meta, n)
			delete(future, n)
		}
	}
	actions := []action{configAction}
	for _, n := range sorted(removed, true) {
		actions = append(actions, action{path: filepath.Join(m.c.Vault, n)})
		fmt.Fprintln(m.out, "delete "+n)
	}
	mb := encoded(Record{Version: 2, Entries: meta})
	md := bytesEntry(mb)
	actions = append(actions, action{filepath.Join(m.c.Vault, ".sfm.json"), &md, mb})
	baseEntries := clone(m.base.Entries)
	baseMeta := clone(m.base.Metadata)
	for n := range baseEntries {
		if within(n, name) {
			delete(baseEntries, n)
		}
	}
	for n := range baseMeta {
		if within(n, name) {
			delete(baseMeta, n)
		}
	}
	bb := encoded(Record{Version: 2, Entries: baseEntries, Metadata: baseMeta})
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
	if e = safe(p); e != nil {
		return "", e
	}
	return p, nil
}
