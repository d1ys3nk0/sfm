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
	c          *Config
	meta, base Record
	state      string
	hasBase    bool
	out        io.Writer
	color      bool
}

func newManager(c *Config, out io.Writer) (*manager, error) {
	s, e := StateDir(c.Vault)
	if e != nil {
		return nil, e
	}
	m := &manager{c: c, state: s, out: out, hasBase: exists(filepath.Join(s, "baseline.json"))}
	m.meta, e = readRecord(filepath.Join(c.Vault, ".sfm.json"), false)
	if e != nil {
		return nil, e
	}
	m.base, e = readRecord(filepath.Join(s, "baseline.json"), true)
	if e != nil {
		return nil, e
	}
	for _, record := range []Record{m.meta, m.base} {
		for n, d := range record.Entries {
			if _, e = c.destination(n); e != nil {
				return nil, e
			}
			if d.Type != "file" && d.Type != "dir" && d.Type != "link" || d.Mode > 07777 {
				return nil, fmt.Errorf("invalid metadata entry: %s", n)
			}
		}
	}
	return m, nil
}
func (m *manager) vaultEntries() (map[string]Entry, error) {
	v := map[string]Entry{}
	for _, ns := range []string{"home", "root"} {
		p := filepath.Join(m.c.Vault, ns)
		if !exists(p) {
			continue
		}
		s, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("vault namespace must be a directory: %s", ns)
		}
		if e = scanTree(p, func(path string, d Entry) error {
			if path != p {
				n, e := filepath.Rel(m.c.Vault, path)
				if e != nil {
					return e
				}
				v[n] = d
			}
			return nil
		}); e != nil {
			return nil, e
		}
	}
	return v, nil
}
func (m *manager) selected(v map[string]Entry) map[string]Entry {
	result := map[string]Entry{}
	for n, d := range v {
		if m.c.selected(n, d.Type == "dir") {
			if saved, ok := m.meta.Entries[n]; ok {
				d.Mode = saved.Mode
			}
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
		if old, ok := m.base.Entries[n]; !ok || old != physical || m.base.Metadata[n] != m.meta.Entries[n] {
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
	updated := clone(m.meta.Entries)
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
		if e = observations.add(filepath.Join(m.c.Vault, n)); e != nil {
			return e
		}
	}
	for n := range desired {
		p, _ := m.c.destination(n)
		for _, path := range []string{p, filepath.Join(m.c.Vault, n)} {
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
		actions = append(actions, action{path: filepath.Join(m.c.Vault, n)})
		delete(updated, n)
		delete(future, n)
		fmt.Fprintln(m.out, "delete "+n)
		d := remove[n]
		if e = m.describeChange(filepath.Join(m.c.Vault, n), "", &d, nil, nil, showDiff); e != nil {
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
			actions = append(actions, action{filepath.Join(m.c.Vault, n), &copyD, b})
			verb := "create "
			if ok {
				verb = "replace "
			}
			fmt.Fprintln(m.out, verb+n)
			var previous *Entry
			if ok {
				previous = &before
			}
			if e = m.describeChange(filepath.Join(m.c.Vault, n), p, previous, &d, b, showDiff); e != nil {
				return e
			}
		}
		updated[n] = d
		future[n] = d
	}
	meta := Record{Version: 2, Entries: updated}
	mb := encoded(meta)
	mp := filepath.Join(m.c.Vault, ".sfm.json")
	oldMeta, _ := os.ReadFile(mp)
	if string(oldMeta) != string(mb) {
		d := bytesEntry(mb)
		actions = append(actions, action{mp, &d, mb})
	}
	base := Record{Version: 2, Entries: future, Metadata: updated}
	if scope != "" && m.hasBase {
		base.Entries = clone(m.base.Entries)
		base.Metadata = clone(m.base.Metadata)
		for n := range base.Entries {
			if within(n, scope) {
				delete(base.Entries, n)
			}
		}
		for n := range base.Metadata {
			if within(n, scope) {
				delete(base.Metadata, n)
			}
		}
		for n, d := range future {
			if within(n, scope) {
				base.Entries[n] = d
			}
		}
		for n, d := range updated {
			if within(n, scope) {
				base.Metadata[n] = d
			}
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
	if e = apply(actions); e != nil {
		return e
	}
	m.meta = meta
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
	selected := m.selected(v)
	type change struct {
		name   string
		action action
		before *Entry
	}
	var changes []change
	// Freeze every source and destination before displaying or asking anything.
	for _, n := range sorted(v, false) {
		if e = observations.add(filepath.Join(m.c.Vault, n)); e != nil {
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
		b, e := payload(filepath.Join(m.c.Vault, n), v[n])
		if e != nil {
			return e
		}
		copyD := d
		changes = append(changes, change{n, action{p, &copyD, b}, before})
	}
	matched := true
	for _, n := range sorted(m.base.Entries, false) {
		d := m.base.Entries[n]
		if _, ok := v[n]; !ok && m.c.selected(n, d.Type == "dir") {
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
		if e = m.describeChange(ch.action.path, filepath.Join(m.c.Vault, ch.name), ch.before, ch.action.want, ch.action.data, showDiff); e != nil {
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
		b := encoded(Record{Version: 2, Entries: v, Metadata: m.meta.Entries})
		d := bytesEntry(b)
		actions = append(actions, action{filepath.Join(m.state, "baseline.json"), &d, b})
	}
	if e = observations.check(); e != nil {
		return e
	}
	return apply(actions)
}
func (m *manager) inspect() (int, error) {
	v, e := m.vaultEntries()
	if e != nil {
		return 2, e
	}
	_, _, warnings, e := m.sourceEntries()
	if e != nil {
		return 2, e
	}
	findings := false
	for _, w := range warnings {
		fmt.Fprintln(m.out, "WARNING "+w)
		findings = true
	}
	for _, n := range sorted(v, false) {
		d := v[n]
		if d.Type != "dir" && !m.c.selected(n, false) {
			fmt.Fprintln(m.out, "unselected vault entry: "+n)
			findings = true
		}
		if saved, ok := m.meta.Entries[n]; ok && (saved.Type != d.Type || saved.Hash != d.Hash || saved.Link != d.Link) {
			fmt.Fprintln(m.out, "metadata mismatch: "+n)
			findings = true
		}
	}
	for _, n := range sorted(m.meta.Entries, false) {
		if _, ok := v[n]; !ok {
			fmt.Fprintln(m.out, "missing metadata payload: "+n)
			findings = true
		}
	}
	if findings {
		return 1, nil
	}
	fmt.Fprintln(m.out, "Verification passed")
	return 0, nil
}
