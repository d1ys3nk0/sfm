package sfm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type Entry struct {
	Type string `json:"type"`
	Mode uint32 `json:"mode"`
	Hash string `json:"hash,omitempty"`
	Link string `json:"link,omitempty"`
}
type Record struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

func safe(path string) error {
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e == nil && (!s.IsDir() || s.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("unsafe ancestor: %s", p)
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func exists(path string) bool { _, e := os.Lstat(path); return e == nil }
func entry(path string) (Entry, error) {
	if e := safe(path); e != nil {
		return Entry{}, e
	}
	s, e := os.Lstat(path)
	if e != nil {
		return Entry{}, e
	}
	d := Entry{Mode: posixMode(s.Mode())}
	switch {
	case s.Mode()&os.ModeSymlink != 0:
		d.Type = "link"
		d.Link, e = os.Readlink(path)
	case s.IsDir():
		d.Type = "dir"
	case s.Mode().IsRegular():
		d.Type = "file"
		var b []byte
		b, e = os.ReadFile(path)
		h := sha256.Sum256(b)
		d.Hash = fmt.Sprintf("%x", h)
	case true:
		e = fmt.Errorf("unsupported file type: %s", path)
	}
	return d, e
}
func bytesEntry(b []byte) Entry {
	h := sha256.Sum256(b)
	return Entry{Type: "file", Mode: 0600, Hash: fmt.Sprintf("%x", h)}
}
func atomic(path string, b []byte, mode uint32) error {
	if e := safe(path); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".sfm-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Chmod(fileMode(mode))
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func encoded(r Record) []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}
func readRecord(path string) (Record, error) {
	r := Record{Version: 3, Entries: map[string]Entry{}}
	if !exists(path) {
		return r, nil
	}
	s, e := os.Lstat(path)
	if e != nil {
		return r, e
	}
	if !s.Mode().IsRegular() {
		return r, fmt.Errorf("baseline must be regular: %s", path)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return r, e
	}
	var header struct {
		Version int `json:"version"`
	}
	if e = json.Unmarshal(b, &header); e != nil {
		return r, e
	}
	if header.Version != 3 {
		return r, fmt.Errorf("unsupported baseline version %d: %s; migrate the vault layout and local baseline to version 3 before continuing", header.Version, path)
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return r, e
	}
	if r.Entries == nil {
		return r, fmt.Errorf("baseline entries missing: %s", path)
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return r, fmt.Errorf("trailing baseline content: %s", path)
	}
	return r, nil
}
func sorted(m map[string]Entry, reverse bool) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.Count(names[i], "/"), strings.Count(names[j], "/")
		if a == b {
			if reverse {
				return names[i] > names[j]
			}
			return names[i] < names[j]
		}
		if reverse {
			return a > b
		}
		return a < b
	})
	return names
}
func scanTree(root string, fn func(string, Entry) error) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		v, e := entry(p)
		if e != nil {
			return e
		}
		return fn(p, v)
	})
}
func lock(dir string) (func(), error) {
	if e := safe(filepath.Join(dir, "lock")); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if s, e := os.Lstat(dir); e != nil || !s.IsDir() || s.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("state directory must be owner-private: %s", dir)
	}
	p := filepath.Join(dir, "lock")
	if s, e := os.Lstat(p); e == nil && !s.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe lock file")
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another SFM operation is running")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

type action struct {
	path string
	want *Entry
	data []byte
}
type saved struct {
	path  string
	entry *Entry
	data  []byte
}

func apply(actions []action) (err error) {
	// Skip exact no-op writes, including controls and baseline files.
	pending := make([]action, 0, len(actions))
	for _, a := range actions {
		if !exists(a.path) {
			if a.want != nil {
				pending = append(pending, a)
			}
			continue
		}
		current, e := entry(a.path)
		if e != nil {
			return e
		}
		if a.want != nil && current == *a.want {
			continue
		}
		pending = append(pending, a)
	}
	actions = pending
	var backups []saved
	parents := map[string]bool{}
	seen := map[string]bool{}
	for _, a := range actions {
		if seen[a.path] {
			return fmt.Errorf("duplicate action: %s", a.path)
		}
		seen[a.path] = true
		if e := safe(a.path); e != nil {
			return e
		}
		b := saved{path: a.path}
		if exists(a.path) {
			d, e := entry(a.path)
			if e != nil {
				return e
			}
			b.entry = &d
			if d.Type == "file" {
				b.data, e = os.ReadFile(a.path)
				if e != nil {
					return e
				}
			}
			if a.want != nil && a.want.Type != d.Type {
				return fmt.Errorf("file-type conflict: %s", a.path)
			}
		}
		backups = append(backups, b)
		for p := filepath.Dir(a.path); !exists(p); p = filepath.Dir(p) {
			parents[p] = true
		}
	}
	defer func() {
		if err == nil {
			return
		}
		var failures []string
		for i := len(backups) - 1; i >= 0; i-- {
			b := backups[i]
			if b.entry == nil {
				if e := os.Remove(b.path); e != nil && !os.IsNotExist(e) {
					failures = append(failures, e.Error())
				}
				continue
			}
			if e := restore(b); e != nil {
				failures = append(failures, e.Error())
			}
		}
		ps := make([]string, 0, len(parents))
		for p := range parents {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return len(ps[i]) > len(ps[j]) })
		for _, p := range ps {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				failures = append(failures, e.Error())
			}
		}
		if len(failures) > 0 {
			err = fmt.Errorf("%w; rollback failed: %s", err, strings.Join(failures, "; "))
		}
	}()
	for _, a := range actions {
		if err = writeAction(a); err != nil {
			return err
		}
	}
	for i := len(actions) - 1; i >= 0; i-- {
		a := actions[i]
		if a.want != nil && a.want.Type == "dir" {
			if err = os.Chmod(a.path, fileMode(a.want.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}
func writeAction(a action) error {
	if e := safe(a.path); e != nil {
		return e
	}
	if a.want == nil {
		e := os.Remove(a.path)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	switch a.want.Type {
	case "file":
		return atomic(a.path, a.data, a.want.Mode)
	case "dir":
		return os.MkdirAll(a.path, 0700)
	case "link":
		if e := os.MkdirAll(filepath.Dir(a.path), 0700); e != nil {
			return e
		}
		f, e := os.CreateTemp(filepath.Dir(a.path), ".sfm-link-*")
		if e != nil {
			return e
		}
		p := f.Name()
		if e = f.Close(); e != nil {
			return e
		}
		_ = os.Remove(p)
		defer os.Remove(p)
		if e = os.Symlink(a.want.Link, p); e != nil {
			return e
		}
		return os.Rename(p, a.path)
	}
	return fmt.Errorf("unknown entry type")
}

func restore(b saved) error {
	if e := writeAction(action{b.path, b.entry, b.data}); e != nil {
		return e
	}
	if b.entry.Type == "dir" {
		return os.Chmod(b.path, fileMode(b.entry.Mode))
	}
	return nil
}
func posixMode(m os.FileMode) uint32 {
	n := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		n |= 04000
	}
	if m&os.ModeSetgid != 0 {
		n |= 02000
	}
	if m&os.ModeSticky != 0 {
		n |= 01000
	}
	return n
}
func fileMode(n uint32) os.FileMode {
	m := os.FileMode(n & 0777)
	if n&04000 != 0 {
		m |= os.ModeSetuid
	}
	if n&02000 != 0 {
		m |= os.ModeSetgid
	}
	if n&01000 != 0 {
		m |= os.ModeSticky
	}
	return m
}
