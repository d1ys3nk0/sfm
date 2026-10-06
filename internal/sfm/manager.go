package sfm

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type manager struct {
	c       *Config
	base    Record
	state   string
	hasBase bool
	out     io.Writer
	color   bool
}

func newManager(c *Config, out io.Writer) (*manager, error) {
	s, e := StateDir(c.Vault)
	if e != nil {
		return nil, e
	}
	m := &manager{c: c, state: s, out: out, hasBase: exists(filepath.Join(s, "baseline.json"))}
	m.base, e = readRecord(filepath.Join(s, "baseline.json"))
	if e != nil {
		return nil, e
	}
	for n, d := range m.base.Entries {
		if _, e = c.destination(n); e != nil {
			return nil, e
		}
		if d.Type != "file" && d.Type != "dir" && d.Type != "link" || d.Mode > 07777 {
			return nil, fmt.Errorf("invalid baseline entry: %s", n)
		}
	}
	return m, nil
}
func (m *manager) vaultEntries() (map[string]Entry, error) {
	v := map[string]Entry{}
	if !exists(m.c.Vault) {
		return v, nil
	}
	e := scanTree(m.c.Vault, func(path string, d Entry) error {
		if path == m.c.Vault {
			if d.Type != "dir" {
				return fmt.Errorf("vault must be a real directory")
			}
			return nil
		}
		rel, e := filepath.Rel(m.c.Vault, path)
		if e != nil {
			return e
		}
		if rel == "_" {
			if d.Type != "dir" {
				return fmt.Errorf("vault _ must be a real directory")
			}
			return nil
		}
		name, e := m.c.vaultName(path)
		if e != nil {
			return e
		}
		v[name] = d
		return nil
	})
	return v, e
}
func (m *manager) selected(v map[string]Entry) map[string]Entry {
	result := map[string]Entry{}
	for n, d := range v {
		if m.c.selected(n, d.Type == "dir") {
			result[n] = d
		}
	}
	return result
}
func (c *Config) roots() ([]string, []string, error) {
	set := map[string]bool{}
	var warnings []string
	for _, r := range c.rules {
		if !r.include {
			continue
		}
		base := c.home
		if r.ns == "root" {
			base = "/"
		}
		var found []string
		if !r.wildcard {
			p := filepath.Join(base, unescape(r.literal))
			if e := safe(p); e != nil {
				return nil, nil, e
			}
			if exists(p) {
				s, e := os.Lstat(p)
				if e != nil {
					return nil, nil, e
				}
				if !r.dir || s.IsDir() && s.Mode()&os.ModeSymlink == 0 {
					found = append(found, p)
				}
			}
		} else {
			prefix := wildcardPrefix(r.literal)
			maxDepth := strings.Count(r.literal, "/") + 1
			recursive := false
			for _, component := range strings.Split(r.literal, "/") {
				if component == "**" {
					recursive = true
				}
			}
			start := filepath.Join(base, unescape(prefix))
			if prefix != "" && !strings.HasSuffix(prefix, "/") {
				start = filepath.Dir(start)
			}
			if exists(start) {
				e := filepath.WalkDir(start, func(p string, d os.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					n := c.name(p)
					parts := strings.SplitN(n, "/", 2)
					matches := p != base && len(parts) == 2 && parts[0] == r.ns && r.matches(parts[1], d.IsDir())
					if within(p, c.Vault) {
						if matches {
							return fmt.Errorf("target selects vault: %s", p)
						}
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
					if !matches && d.IsDir() && !recursive && len(parts) == 2 && parts[1] != "" && strings.Count(parts[1], "/")+1 >= maxDepth {
						return filepath.SkipDir
					}
					if matches {
						found = append(found, p)
						if d.IsDir() {
							return filepath.SkipDir
						}
					}
					return nil
				})
				if e != nil {
					return nil, nil, e
				}
			}
		}
		if len(found) == 0 {
			warnings = append(warnings, "missing or invalid target: "+r.literal)
		}
		for _, p := range found {
			if _, e := c.destination(c.name(p)); e != nil {
				return nil, nil, e
			}
			if within(p, c.Vault) || within(c.Vault, p) {
				return nil, nil, fmt.Errorf("target selects vault: %s", p)
			}
			if e := safe(p); e != nil {
				return nil, nil, e
			}
			set[p] = true
		}
	}
	roots := make([]string, 0, len(set))
	for p := range set {
		roots = append(roots, p)
	}
	sort.Strings(roots)
	return roots, warnings, nil
}
func (m *manager) sourceEntries() (map[string]Entry, []string, []string, error) {
	return m.sourceEntriesScope("")
}
func (m *manager) sourceEntriesScope(scope string) (map[string]Entry, []string, []string, error) {
	var roots, warnings []string
	var e error
	if scope != "" {
		p, err := m.c.destination(scope)
		if err != nil {
			return nil, nil, nil, err
		}
		if e := safe(p); e != nil {
			return nil, nil, nil, e
		}
		roots = []string{p}
	} else {
		roots, warnings, e = m.c.roots()
		if e != nil {
			return nil, nil, nil, e
		}
	}
	v := map[string]Entry{}
	for _, root := range roots {
		e = filepath.WalkDir(root, func(p string, info os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if within(p, m.c.Vault) {
				return fmt.Errorf("vault self-selection: %s", p)
			}
			n := m.c.name(p)
			if info.IsDir() && m.c.prune(n) {
				return filepath.SkipDir
			}
			if !m.c.selected(n, info.IsDir()) {
				return nil
			}
			if _, e := m.c.destination(n); e != nil {
				return e
			}
			d, e := entry(p)
			if e != nil {
				return e
			}
			v[n] = d
			return nil
		})
		if e != nil {
			return nil, nil, nil, e
		}
	}
	return v, roots, warnings, nil
}
func (m *manager) reconcile(v map[string]Entry, scope string) error {
	if !m.hasBase && len(m.selected(v)) > 0 {
		return fmt.Errorf("vault requires reconciliation: review install --dry --diff, then install")
	}
	for n, d := range m.selected(v) {
		if scope != "" && !within(n, scope) {
			continue
		}
		physical := v[n]
		if old, ok := m.base.Entries[n]; !ok || old != physical {
			p, _ := m.c.destination(n)
			current, e := entry(p)
			if e != nil || current != d {
				return fmt.Errorf("vault changed since reconciliation: %s; review install --dry --diff, then install or install --force", n)
			}
		}
	}
	for n, d := range m.base.Entries {
		if scope != "" && !within(n, scope) {
			continue
		}
		if _, ok := v[n]; !ok && m.c.selected(n, d.Type == "dir") {
			p, _ := m.c.destination(n)
			if exists(p) {
				return fmt.Errorf("vault deletion requires deliberate installed-copy removal: %s", n)
			}
		}
	}
	return nil
}
func payload(path string, d Entry) ([]byte, error) {
	now, e := entry(path)
	if e != nil {
		return nil, e
	}
	if now != d {
		return nil, fmt.Errorf("source changed during scan: %s", path)
	}
	if d.Type != "file" {
		return nil, nil
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if bytesEntry(b).Hash != d.Hash {
		return nil, fmt.Errorf("source changed during scan: %s", path)
	}
	return b, nil
}
func clone(v map[string]Entry) map[string]Entry {
	r := map[string]Entry{}
	for n, d := range v {
		r[n] = d
	}
	return r
}
func (m *manager) snapshot(dry, showDiff bool, scope string, extra []action) error {
	observations, e := m.observeControls()
	if e != nil {
		return e
	}
	old, e := m.vaultEntries()
	if e != nil {
		return e
	}
	if e = m.reconcile(old, scope); e != nil {
		return e
	}
	desired, roots, warnings, e := m.sourceEntriesScope(scope)
	if e != nil {
		return e
	}
	if scope != "" {
		for n := range desired {
			if !within(n, scope) {
				delete(desired, n)
			}
		}
	}
	future := clone(old)
	remove := map[string]Entry{}
	for n, d := range old {
		if scope != "" && !within(n, scope) {
			continue
		}
		if _, ok := desired[n]; ok || !m.c.selected(n, d.Type == "dir") {
			continue
		}
		p, _ := m.c.destination(n)
		for _, r := range roots {
			st, e := os.Lstat(r)
			if e != nil {
				return e
			}
			if st.IsDir() && within(p, r) && p != r {
				remove[n] = d
				break
			}
		}
	} // Preserve directories containing retained children.
	for n, d := range remove {
		if d.Type == "dir" {
			for child := range old {
				if child != n && within(child, n) {
					if _, gone := remove[child]; !gone {
						delete(remove, n)
						break
					}
				}
			}
		}
	}
	for n := range old {
		if e = observations.add(m.c.vaultPath(n)); e != nil {
			return e
		}
	}
	for n := range desired {
		p, _ := m.c.destination(n)
		for _, path := range []string{p, m.c.vaultPath(n)} {
			if e = observations.add(path); e != nil {
				return e
			}
		}
	}
	for n := range remove {
		p, _ := m.c.destination(n)
		if e = observations.add(p); e != nil {
			return e
		}
	}
	actions := append([]action{}, extra...)
	for _, n := range sorted(remove, true) {
		actions = append(actions, action{path: m.c.vaultPath(n)})
		delete(future, n)
		fmt.Fprintln(m.out, "delete "+strings.TrimPrefix(m.c.vaultPath(n), m.c.Vault+"/"))
		d := remove[n]
		if e = m.describeChange(m.c.vaultPath(n), "", &d, nil, nil, showDiff); e != nil {
			return e
		}
	}
	for _, n := range sorted(desired, false) {
		d := desired[n]
		before, ok := old[n]
		if ok && before.Type != d.Type {
			return fmt.Errorf("file-type conflict: %s", n)
		}
		if !ok || before != d {
			p, _ := m.c.destination(n)
			b, e := payload(p, d)
			if e != nil {
				return e
			}
			copyD := d
			actions = append(actions, action{m.c.vaultPath(n), &copyD, b})
			verb := "create "
			if ok {
				verb = "replace "
			}
			fmt.Fprintln(m.out, verb+strings.TrimPrefix(m.c.vaultPath(n), m.c.Vault+"/"))
			var previous *Entry
			if ok {
				previous = &before
			}
			if e = m.describeChange(m.c.vaultPath(n), p, previous, &d, b, showDiff); e != nil {
				return e
			}
		}
		future[n] = d
	}
	// Atomic copies create omitted selection ancestors with private modes.
	// Record these actual payload directories in the private baseline too.
	for n := range desired {
		for p := filepath.Dir(m.c.vaultPath(n)); p != m.c.Vault; p = filepath.Dir(p) {
			parent, e := m.c.vaultName(p)
			if e != nil {
				return e
			}
			if parent != "" {
				if _, ok := future[parent]; !ok {
					future[parent] = Entry{Type: "dir", Mode: 0700}
				}
			}
		}
	}
	base := Record{Version: 3, Entries: clone(m.base.Entries)}
	for n, d := range base.Entries {
		if (scope == "" || within(n, scope)) && m.c.selected(n, d.Type == "dir") {
			if _, ok := future[n]; !ok {
				delete(base.Entries, n)
			}
		}
	}
	for n, d := range future {
		_, existed := old[n]
		if !existed || (scope == "" || within(n, scope)) && m.c.selected(n, d.Type == "dir") {
			base.Entries[n] = d
		}
	}
	bb := encoded(base)
	bd := bytesEntry(bb)
	actions = append(actions, action{filepath.Join(m.state, "baseline.json"), &bd, bb})
	for _, w := range warnings {
		fmt.Fprintln(m.out, "WARNING "+w)
	}
	if dry {
		return nil
	}
	for _, n := range sorted(desired, false) {
		p, _ := m.c.destination(n)
		if _, e = payload(p, desired[n]); e != nil {
			return e
		}
	}
	if e = observations.check(); e != nil {
		return e
	}
	if e = m.checkVault(old); e != nil {
		return e
	}
	if e = apply(actions); e != nil {
		return e
	}
	m.base = base
	m.hasBase = true
	return nil
}
func (m *manager) install(force, dry, showDiff bool, in io.Reader) error {
	observations, e := m.observeControls()
	if e != nil {
		return e
	}
	v, e := m.vaultEntries()
	if e != nil {
		return e
	}
	selected := v
	type change struct {
		name   string
		action action
		before *Entry
	}
	var changes []change
	// Freeze every source and destination before displaying or asking anything.
	for _, n := range sorted(v, false) {
		if e = observations.add(m.c.vaultPath(n)); e != nil {
			return e
		}
	}
	for _, n := range sorted(selected, false) {
		d := selected[n]
		p, _ := m.c.destination(n)
		if e = observations.add(p); e != nil {
			return e
		}
		before := observations[p]
		if before != nil {
			if before.Type != d.Type {
				return fmt.Errorf("file-type conflict: %s", p)
			}
			if *before == d {
				continue
			}
		}
		b, e := payload(m.c.vaultPath(n), v[n])
		if e != nil {
			return e
		}
		copyD := d
		changes = append(changes, change{n, action{p, &copyD, b}, before})
	}
	matched := true
	for _, n := range sorted(m.base.Entries, false) {
		if _, ok := v[n]; !ok {
			p, _ := m.c.destination(n)
			if e = observations.add(p); e != nil {
				return e
			}
			if observations[p] != nil {
				matched = false
				fmt.Fprintln(m.out, "vault deletion requires manual installed-copy removal: "+p)
			}
		}
	}
	var actions []action
	reader := bufio.NewReader(in)
	for _, ch := range changes {
		verb := "create "
		if ch.before != nil {
			verb = "replace "
		}
		fmt.Fprintln(m.out, verb+ch.action.path)
		if e = m.describeChange(ch.action.path, m.c.vaultPath(ch.name), ch.before, ch.action.want, ch.action.data, showDiff); e != nil {
			return e
		}
		if !dry && !force && ch.before != nil {
			yes, e := approve(reader, m.out, ch.action.path)
			if e != nil {
				return e
			}
			if !yes {
				matched = false
				fmt.Fprintln(m.out, "skip "+ch.action.path)
				continue
			}
		}
		actions = append(actions, ch.action)
	}
	if dry {
		return nil
	}
	if matched {
		b := encoded(Record{Version: 3, Entries: v})
		d := bytesEntry(b)
		actions = append(actions, action{filepath.Join(m.state, "baseline.json"), &d, b})
	}
	if e = observations.check(); e != nil {
		return e
	}
	if e = m.checkVault(v); e != nil {
		return e
	}
	return apply(actions)
}
func (m *manager) inspect() (int, error) {
	if _, e := m.vaultEntries(); e != nil {
		return 2, e
	}
	fmt.Fprintln(m.out, "Verification passed")
	return 0, nil
}
