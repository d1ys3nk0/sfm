package sfm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (m *manager) describeDifference(name, path string, want, current Entry) error {
	if want.Type != current.Type {
		fmt.Fprintf(m.out, "type %s -> %s\n", want.Type, current.Type)
		return nil
	}
	if want.Mode != current.Mode {
		fmt.Fprintf(m.out, "permissions %#o -> %#o\n", want.Mode, current.Mode)
	}
	if want.Type == "link" && want.Link != current.Link {
		fmt.Fprintf(m.out, "link %s -> %s\n", want.Link, current.Link)
	}
	if want.Type != "file" || want.Hash == current.Hash {
		return nil
	}
	before, e := os.ReadFile(filepath.Join(m.c.Vault, name))
	if e != nil {
		return e
	}
	after, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if !utf8.Valid(before) || !utf8.Valid(after) || strings.ContainsRune(string(before)+string(after), 0) {
		fmt.Fprintln(m.out, "binary contents differ")
		return nil
	}
	oldLines, newLines := lines(string(before)), lines(string(after))
	fmt.Fprintf(m.out, "--- vault/%s\n+++ %s\n@@ -%s +%s @@\n", name, path, lineRange(len(oldLines)), lineRange(len(newLines)))
	for _, line := range oldLines {
		printLine(m, "-", line)
	}
	for _, line := range newLines {
		printLine(m, "+", line)
	}
	return nil
}
func lines(s string) []string {
	if s == "" {
		return nil
	}
	v := strings.SplitAfter(s, "\n")
	if v[len(v)-1] == "" {
		v = v[:len(v)-1]
	}
	return v
}
func lineRange(n int) string {
	if n == 0 {
		return "0,0"
	}
	return fmt.Sprintf("1,%d", n)
}
func printLine(m *manager, prefix, line string) {
	fmt.Fprint(m.out, prefix+line)
	if !strings.HasSuffix(line, "\n") {
		fmt.Fprintln(m.out, "\n\\ No newline at end of file")
	}
}
