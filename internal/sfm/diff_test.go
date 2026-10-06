package sfm

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestDiffCompactHunks(t *testing.T) {
	f := setup(t, "~/file")
	var values []string
	for i := 1; i <= 30; i++ {
		values = append(values, fmt.Sprintf("line %d\n", i))
	}
	file := f.home + "/file"
	f.write(file, strings.Join(values, ""))
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	values[5], values[24] = "first change\n", "second change\n"
	f.write(file, strings.Join(values, ""))
	c, o, e = f.run("snapshot", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	for _, want := range []string{"@@ -3,7 +3,7 @@", "@@ -22,7 +22,7 @@", "-line 6\n+first change\n", "-line 25\n+second change\n"} {
		if !strings.Contains(o, want) {
			t.Fatalf("missing %q:\n%s", want, o)
		}
	}
	if strings.Contains(o, "line 15") || strings.Contains(o, "\x1b[") {
		t.Fatal(o)
	}
}

func TestUnifiedRangesAndNewlines(t *testing.T) {
	cases := []struct{ before, after, want string }{
		{"", "new\n", "@@ -0,0 +1 @@\n+new\n"},
		{"old\n", "", "@@ -1 +0,0 @@\n-old\n"},
		{"old\n", "new\n", "@@ -1 +1 @@\n-old\n+new\n"},
		{"same", "same\n", "@@ -1 +1 @@\n-same\n\\ No newline at end of file\n+same\n"},
		{"old\n", "new", "+new\n\\ No newline at end of file\n"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		writeUnified(&out, "file", "/file", lines(tc.before), lines(tc.after), false)
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("want %q:\n%s", tc.want, out.String())
		}
	}
}

func TestDiffColorModes(t *testing.T) {
	f := setup(t, "~/file")
	file := f.home + "/file"
	f.write(file, "old\n")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	f.write(file, "new\n")
	for _, mode := range []string{"auto", "never", "always"} {
		c, o, e = f.run("snapshot", "--dry", "--diff", "--color="+mode)
		requireCode(t, 0, c, o, e)
		if strings.Contains(o, "\x1b[") != (mode == "always") {
			t.Fatalf("mode %s: %q", mode, o)
		}
		if mode == "always" {
			for _, want := range []string{"\x1b[1m---", "\x1b[36m@@", "\x1b[31m-old", "\x1b[32m+new"} {
				if !strings.Contains(o, want) {
					t.Fatalf("missing %q: %q", want, o)
				}
			}
		}
	}
	c, o, e = f.run("snapshot", "--dry", "--diff", "--color")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "\x1b[") {
		t.Fatal(o)
	}
	for _, args := range [][]string{{"diff", "--color=wrong"}, {"verify", "--color=never"}} {
		c, o, e = f.run(args...)
		requireCode(t, 2, c, o, e)
	}
	read, write, err := os.Pipe()
	must(t, err)
	defer read.Close()
	defer write.Close()
	if terminalOutput(write) {
		t.Fatal("pipe classified as terminal")
	}
	null, err := os.Open(os.DevNull)
	must(t, err)
	defer null.Close()
	if terminalOutput(null) {
		t.Fatal("null device classified as terminal")
	}
	f.write(file, "\x00binary")
	c, o, e = f.run("snapshot", "--dry", "--diff", "--color=always")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "binary contents differ") || strings.Contains(o, "@@") {
		t.Fatal(o)
	}
}

// Apply the public unified output to synthetic input, validating every old
// context/deletion and range. Random repeated lines exercise ambiguous matches.
func applyUnified(t *testing.T, before, patch string) string {
	t.Helper()
	old := lines(before)
	var result []string
	cursor, newCursor := 0, 0
	rows := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	for i := 2; i < len(rows); {
		var oldStart, oldCount, newStart, newCount int
		fields := strings.Fields(rows[i])
		if len(fields) != 4 || fields[0] != "@@" {
			t.Fatalf("bad hunk: %q", rows[i])
		}
		parse := func(value string) (int, int) {
			parts := strings.Split(value[1:], ",")
			start, err := strconv.Atoi(parts[0])
			must(t, err)
			count := 1
			if len(parts) == 2 {
				count, err = strconv.Atoi(parts[1])
				must(t, err)
			}
			if count != 0 {
				start--
			}
			return start, count
		}
		oldStart, oldCount = parse(fields[1])
		newStart, newCount = parse(fields[2])
		if oldStart < cursor || oldStart > len(old) {
			t.Fatalf("bad old start %d: %s", oldStart, patch)
		}
		result = append(result, old[cursor:oldStart]...)
		newCursor += oldStart - cursor
		if newStart != newCursor {
			t.Fatalf("bad new start %d want %d: %s", newStart, newCursor, patch)
		}
		cursor = oldStart
		i++
		consumed, produced := 0, 0
		for i < len(rows) && !strings.HasPrefix(rows[i], "@@") {
			row := rows[i]
			if len(row) == 0 {
				t.Fatal("empty patch row")
			}
			value := row[1:] + "\n"
			i++
			if i < len(rows) && rows[i] == "\\ No newline at end of file" {
				value = strings.TrimSuffix(value, "\n")
				i++
			}
			if row[0] != '+' {
				if cursor >= len(old) || old[cursor] != value {
					t.Fatalf("old mismatch at %d: %s", cursor, patch)
				}
				cursor++
				consumed++
			}
			if row[0] != '-' {
				result = append(result, value)
				newCursor++
				produced++
			}
		}
		if consumed != oldCount || produced != newCount {
			t.Fatalf("bad hunk counts: %s", patch)
		}
	}
	result = append(result, old[cursor:]...)
	return strings.Join(result, "")
}

func TestUnifiedRoundTrip(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		makeText := func() string {
			var out strings.Builder
			for n := random.Intn(30); n > 0; n-- {
				fmt.Fprintf(&out, "%d\n", random.Intn(5))
			}
			text := out.String()
			if random.Intn(2) == 0 {
				text = strings.TrimSuffix(text, "\n")
			}
			return text
		}
		before, after := makeText(), makeText()
		if before == after {
			continue
		}
		var patch bytes.Buffer
		writeUnified(&patch, "file", "/file", lines(before), lines(after), false)
		if got := applyUnified(t, before, patch.String()); got != after {
			t.Fatalf("got %q want %q\n%s", got, after, patch.String())
		}
		old, next := lines(before), lines(after)
		// Small test-only LCS oracle verifies shortest edits independently of
		// the production linear-space Myers search and its tie choices.
		lengths := make([]int, len(next)+1)
		for _, a := range old {
			previous := 0
			for j, b := range next {
				saved := lengths[j+1]
				if a == b {
					lengths[j+1] = previous + 1
				} else {
					lengths[j+1] = max(lengths[j], lengths[j+1])
				}
				previous = saved
			}
		}
		wantEdits := len(old) + len(next) - 2*lengths[len(next)]
		gotEdits := 0
		for _, row := range strings.Split(patch.String(), "\n")[2:] {
			if strings.HasPrefix(row, "+") || strings.HasPrefix(row, "-") {
				gotEdits++
			}
		}
		if gotEdits != wantEdits {
			t.Fatalf("edits=%d want shortest=%d\n%s", gotEdits, wantEdits, patch.String())
		}
	}
}

func TestDiffLongRepeatedInput(t *testing.T) {
	before := strings.Repeat("same\n", 20000)
	after := strings.Repeat("same\n", 10000) + "inserted\n" + strings.Repeat("same\n", 10000)
	var patch bytes.Buffer
	writeUnified(&patch, "file", "/file", lines(before), lines(after), false)
	if len(patch.String()) > 200 {
		t.Fatalf("diff not compact: %d bytes", patch.Len())
	}
	if got := applyUnified(t, before, patch.String()); got != after {
		t.Fatal("long input patch failed")
	}
}
