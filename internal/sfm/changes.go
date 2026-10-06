package sfm

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// observed freezes the paths used to plan an operation, including absent paths.
// Recheck them after user input, before handing effects to the transaction.
type observed map[string]*Entry

func (o observed) add(path string) error {
	if _, ok := o[path]; ok {
		return nil
	}
	d, e := entry(path)
	if os.IsNotExist(e) {
		o[path] = nil
		return nil
	}
	if e != nil {
		return e
	}
	o[path] = &d
	return nil
}

func (o observed) check() error {
	for path, before := range o {
		now, e := entry(path)
		if before == nil && os.IsNotExist(e) {
			continue
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if before == nil || e != nil || now != *before {
			return fmt.Errorf("path changed during operation: %s", path)
		}
	}
	return nil
}

func (m *manager) observeControls() (observed, error) {
	o := observed{}
	for _, p := range []string{m.c.source, filepath.Join(m.state, "baseline.json")} {
		if e := o.add(p); e != nil {
			return nil, e
		}
	}
	return o, nil
}

func approve(in *bufio.Reader, out io.Writer, path string) (bool, error) {
	for {
		fmt.Fprintf(out, "Write %s? [y/n] ", path)
		answer, e := in.ReadString('\n')
		if e != nil {
			return false, fmt.Errorf("approval input: %w", e)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y":
			return true, nil
		case "n":
			return false, nil
		default:
			fmt.Fprintln(out, "Please enter y or n.")
		}
	}
}

// Check the complete payload inventory as well, including paths added while asking.
func (m *manager) checkVault(before map[string]Entry) error {
	now, e := m.vaultEntries()
	if e != nil {
		return e
	}
	if len(now) != len(before) {
		return fmt.Errorf("vault changed during operation")
	}
	for n, d := range before {
		if now[n] != d {
			return fmt.Errorf("vault changed during operation: %s", n)
		}
	}
	return nil
}
