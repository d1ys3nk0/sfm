package sfm

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// describeChange renders current destination -> desired contents.
func (m *manager) describeChange(path, source string, before, after *Entry, data []byte, showDiff bool) error {
	if before == nil && after != nil {
		fmt.Fprintf(m.out, "permissions /dev/null -> %#o\n", after.Mode)
	}
	if before != nil && after != nil && before.Mode != after.Mode {
		fmt.Fprintf(m.out, "permissions %#o -> %#o\n", before.Mode, after.Mode)
	}
	if after != nil && after.Type == "link" {
		old := "/dev/null"
		if before != nil {
			old = before.Link
		}
		if before == nil || old != after.Link {
			fmt.Fprintf(m.out, "link %s -> %s\n", old, after.Link)
		}
	}
	if before != nil && before.Type == "link" && after == nil {
		fmt.Fprintf(m.out, "link %s -> /dev/null\n", before.Link)
	}
	if !showDiff || (after != nil && after.Type != "file") || (before != nil && before.Type != "file") {
		return nil
	}
	if before != nil && after != nil && before.Hash == after.Hash {
		return nil
	}
	var old []byte
	if before != nil {
		var e error
		old, e = payload(path, *before)
		if e != nil {
			return e
		}
	}
	if !utf8.Valid(old) || !utf8.Valid(data) || strings.ContainsRune(string(old)+string(data), 0) {
		fmt.Fprintln(m.out, "binary contents differ")
		return nil
	}
	oldLabel, newLabel := path, source
	if before == nil {
		oldLabel = "/dev/null"
	}
	if after == nil {
		newLabel = "/dev/null"
	}
	writeUnified(m.out, oldLabel, newLabel, lines(string(old)), lines(string(data)), m.color)
	if len(old) == 0 && len(data) == 0 {
		fmt.Fprintln(m.out, "empty file")
	}
	return nil
}

type diffLine struct {
	kind byte
	text string
}

// lineDiff uses Myers' bidirectional search. Splitting at the middle of an
// edit path keeps auxiliary memory linear instead of retaining every frontier.
func lineDiff(before, after []string) []diffLine {
	result := make([]diffLine, 0, len(before)+len(after))
	var compare func([]string, []string)
	emit := func(kind byte, values []string) {
		for _, value := range values {
			result = append(result, diffLine{kind, value})
		}
	}
	compare = func(a, b []string) {
		start := 0
		for start < len(a) && start < len(b) && a[start] == b[start] {
			start++
		}
		emit(' ', a[:start])
		a, b = a[start:], b[start:]
		end := 0
		for end < len(a) && end < len(b) && a[len(a)-end-1] == b[len(b)-end-1] {
			end++
		}
		suffix := a[len(a)-end:]
		a, b = a[:len(a)-end], b[:len(b)-end]
		switch {
		case len(a) == 0:
			emit('+', b)
		case len(b) == 0:
			emit('-', a)
		default:
			x, y := middleSplit(a, b)
			if (x == 0 && y == 0) || (x == len(a) && y == len(b)) {
				emit('-', a)
				emit('+', b)
			} else {
				compare(a[:x], b[:y])
				compare(a[x:], b[y:])
			}
		}
		emit(' ', suffix)
	}
	compare(before, after)
	return result
}

func middleSplit(a, b []string) (int, int) {
	n, m := len(a), len(b)
	maxD := (n + m + 1) / 2
	offset := maxD + 1
	forward, reverse := make([]int, 2*maxD+3), make([]int, 2*maxD+3)
	for i := range forward {
		forward[i], reverse[i] = -1, -1
	}
	forward[offset+1], reverse[offset+1] = 0, 0
	delta := n - m
	odd := delta%2 != 0
	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			idx := offset + k
			x := forward[idx-1] + 1
			if k == -d || (k != d && forward[idx-1] < forward[idx+1]) {
				x = forward[idx+1]
			}
			y := x - k
			for x < n && y < m && x >= 0 && y >= 0 && a[x] == b[y] {
				x++
				y++
			}
			forward[idx] = x
			rk := delta - k
			if odd && rk >= -(d-1) && rk <= d-1 && reverse[offset+rk] >= 0 && x >= n-reverse[offset+rk] {
				return x, y
			}
		}
		for k := -d; k <= d; k += 2 {
			idx := offset + k
			x := reverse[idx-1] + 1
			if k == -d || (k != d && reverse[idx-1] < reverse[idx+1]) {
				x = reverse[idx+1]
			}
			y := x - k
			for x < n && y < m && x >= 0 && y >= 0 && a[n-x-1] == b[m-y-1] {
				x++
				y++
			}
			reverse[idx] = x
			fk := delta - k
			if !odd && fk >= -d && fk <= d && forward[offset+fk] >= 0 && forward[offset+fk] >= n-x {
				fx := forward[offset+fk]
				return fx, fx - fk
			}
		}
	}
	return n, m
}

func writeUnified(out io.Writer, name, path string, before, after []string, color bool) {
	edits := lineDiff(before, after)
	paint := func(code, text string) string {
		if color {
			return "\x1b[" + code + "m" + text + "\x1b[m"
		}
		return text
	}
	fmt.Fprintln(out, paint("1", "--- "+name))
	fmt.Fprintln(out, paint("1", "+++ "+path))
	oldPos, newPos := 1, 1
	for cursor := 0; cursor < len(edits); {
		first := cursor
		for first < len(edits) && edits[first].kind == ' ' {
			first++
		}
		if first == len(edits) {
			break
		}
		start := max(cursor, first-3)
		for _, line := range edits[cursor:start] {
			if line.kind != '+' {
				oldPos++
			}
			if line.kind != '-' {
				newPos++
			}
		}
		last := first
		for next := first + 1; next < len(edits); next++ {
			if edits[next].kind != ' ' {
				if next-last > 7 {
					break
				}
				last = next
			}
		}
		end := min(len(edits), last+4)
		oldCount, newCount := 0, 0
		for _, line := range edits[start:end] {
			if line.kind != '+' {
				oldCount++
			}
			if line.kind != '-' {
				newCount++
			}
		}
		header := fmt.Sprintf("@@ -%s +%s @@", lineRange(oldPos, oldCount), lineRange(newPos, newCount))
		fmt.Fprintln(out, paint("36", header))
		for _, line := range edits[start:end] {
			code := ""
			if line.kind == '+' {
				code = "32"
			}
			if line.kind == '-' {
				code = "31"
			}
			text := string(line.kind) + strings.TrimSuffix(line.text, "\n")
			if code != "" {
				text = paint(code, text)
			}
			fmt.Fprintln(out, text)
			if !strings.HasSuffix(line.text, "\n") {
				fmt.Fprintln(out, "\\ No newline at end of file")
			}
		}
		oldPos += oldCount
		newPos += newCount
		cursor = end
	}
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
func lineRange(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func terminalOutput(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd())) && os.Getenv("TERM") != "dumb"
}
