package sfm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	home, vault, config, state string
	t                          *testing.T
}

func setup(t *testing.T, patterns ...string) fixture {
	t.Helper()
	root := t.TempDir()
	root, e := filepath.EvalSymlinks(root)
	must(t, e)
	f := fixture{home: filepath.Join(root, "home"), vault: filepath.Join(root, "vault"), config: filepath.Join(root, "config.toml"), state: filepath.Join(root, "state"), t: t}
	must(t, os.MkdirAll(f.home, 0700))
	must(t, os.MkdirAll(f.vault, 0700))
	t.Setenv("HOME", f.home)
	t.Setenv("XDG_STATE_HOME", f.state)
	values := make([]string, len(patterns))
	for i, p := range patterns {
		b, _ := json.Marshal(p)
		values[i] = string(b)
	}
	f.write(f.config, "# retain configuration\nvault = "+quote(f.vault)+"\n\n[targets]\n# retain selection\npatterns = [\n"+strings.Join(values, ",\n")+"\n]\n")
	return f
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func (f fixture) write(path, value string) {
	f.t.Helper()
	must(f.t, os.MkdirAll(filepath.Dir(path), 0700))
	must(f.t, os.WriteFile(path, []byte(value), 0600))
}
func (f fixture) run(args ...string) (int, string, string) {
	f.t.Helper()
	var out, err bytes.Buffer
	code := Run(append([]string{"--config", f.config}, args...), &out, &err)
	return code, out.String(), err.String()
}
func (f fixture) runInput(input string, args ...string) (int, string, string) {
	f.t.Helper()
	var out bytes.Buffer
	code, e := runWithInput(append([]string{"--config", f.config}, args...), strings.NewReader(input), &out)
	if e != nil {
		return 2, out.String(), e.Error()
	}
	return code, out.String(), ""
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func requireCode(t *testing.T, want int, code int, out, err string) {
	t.Helper()
	if code != want {
		t.Fatalf("code=%d want=%d out=%s err=%s", code, want, out, err)
	}
}
func TestSnapshotInstallReconcile(t *testing.T) {
	f := setup(t, "~/settings/")
	file := filepath.Join(f.home, "settings/config")
	f.write(file, "first\n")
	must(t, os.Mkdir(filepath.Join(f.home, "settings/empty"), 0750))
	must(t, os.Symlink("config", filepath.Join(f.home, "settings/link")))
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	saved := filepath.Join(f.vault, "settings/config")
	b, err := os.ReadFile(saved)
	must(t, err)
	if string(b) != "first\n" {
		t.Fatal("capture")
	}
	f.write(saved, "incoming\n")
	c, o, e = f.run("snapshot")
	requireCode(t, 2, c, o, e)
	c, o, e = f.runInput("n\n", "install")
	requireCode(t, 0, c, o, e)
	b, err = os.ReadFile(file)
	must(t, err)
	if string(b) != "first\n" {
		t.Fatal("ordinary install overwrote")
	}
	c, o, e = f.run("install", "--force")
	requireCode(t, 0, c, o, e)
	b, err = os.ReadFile(file)
	must(t, err)
	if string(b) != "incoming\n" {
		t.Fatal("force install")
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.Remove(file))
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	if exists(saved) {
		t.Fatal("selected deletion not captured")
	}
}
func TestDryRunNoMutation(t *testing.T) {
	f := setup(t, "~/file")
	f.write(filepath.Join(f.home, "file"), "payload")
	c, o, e := f.run("snapshot", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "--- /dev/null") || !strings.Contains(o, "+payload") || strings.Contains(o, ".sfm.json") {
		t.Fatal(o)
	}
	if exists(f.state) || exists(filepath.Join(f.vault, "file")) || exists(filepath.Join(f.vault, ".sfm.json")) {
		t.Fatal("dry run wrote files")
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
}
func TestPatternSemantics(t *testing.T) {
	cases := []struct {
		pattern, name string
		dir, want     bool
	}{{"~/foo/*/", "home/foo/file", false, false}, {"~/foo/*/", "home/foo/dir", true, true}, {"~/foo/*/", "home/foo/dir/file", false, true}, {"~/foo/**/bar", "home/foo/bar", false, true}, {"~/foo/**/bar", "home/foo/a/b/bar", false, true}, {"~/a**b", "home/a/x/b", false, false}, {`~/literal\*.txt`, "home/literal*.txt", false, true}, {"~/[ab].txt", "home/a.txt", false, true}}
	for _, v := range cases {
		t.Run(v.pattern+v.name, func(t *testing.T) {
			r, e := compile(v.pattern, "/home/person", "/vault")
			must(t, e)
			c := Config{rules: []rule{*r}}
			if got := c.selected(v.name, v.dir); got != v.want {
				t.Fatalf("got %v want %v", got, v.want)
			}
		})
	}
}
func TestOrderedExclusions(t *testing.T) {
	f := setup(t, "~/settings/", "!~/settings/private/", "~/settings/private/public")
	f.write(filepath.Join(f.home, "settings/file"), "yes")
	f.write(filepath.Join(f.home, "settings/private/secret"), "no")
	f.write(filepath.Join(f.home, "settings/private/public"), "yes")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "settings/private/secret")) || !exists(filepath.Join(f.vault, "settings/private/public")) {
		t.Fatal("ordered selection")
	}
}
func TestMissingRootPreservesCapture(t *testing.T) {
	f := setup(t, "~/settings/")
	f.write(filepath.Join(f.home, "settings/file"), "yes")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.RemoveAll(filepath.Join(f.home, "settings")))
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	if !exists(filepath.Join(f.vault, "settings/file")) {
		t.Fatal("missing root deleted capture")
	}
}
func TestTrackForgetAndComments(t *testing.T) {
	f := setup(t)
	f.write(filepath.Join(f.home, "one"), "one")
	f.write(filepath.Join(f.home, "two"), "two")
	c, o, e := f.run("track", filepath.Join(f.home, "one"))
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("track", filepath.Join(f.home, "two"))
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("forget", filepath.Join(f.home, "one"))
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "one")) || !exists(filepath.Join(f.vault, "two")) || !exists(filepath.Join(f.home, "one")) {
		t.Fatal("scope or source mutation")
	}
	config, err := readConfig(f.config)
	must(t, err)
	if config.selected("home/one", false) || !config.selected("home/two", false) {
		t.Fatal("forget did not update the scoped selection")
	}
	for _, pattern := range config.Targets.Patterns {
		if pattern == "~/one" {
			t.Fatal("forget retained the literal selection")
		}
	}
	original, err := os.ReadFile(filepath.Join(f.home, "one"))
	must(t, err)
	if string(original) != "one" {
		t.Fatal("forget changed the installed original")
	}
	b, err := os.ReadFile(f.config)
	must(t, err)
	if !strings.Contains(string(b), "# retain configuration") || !strings.Contains(string(b), "# retain selection") {
		t.Fatal("lost comments")
	}
}
func TestEmptyBaseline(t *testing.T) {
	f := setup(t)
	for i := 0; i < 2; i++ {
		c, o, e := f.run("snapshot")
		requireCode(t, 0, c, o, e)
	}
}
func TestUnsafeBaseline(t *testing.T) {
	f := setup(t)
	state, e := StateDir(f.vault)
	must(t, e)
	f.write(filepath.Join(state, "baseline.json"), `{"version":3,"entries":{"home/..":{"type":"dir","mode":448}}}`)
	c, o, errOut := f.run("install")
	requireCode(t, 2, c, o, errOut)
}
func TestSymlinkAncestorAndTypeConflict(t *testing.T) {
	f := setup(t, "~/settings/file")
	must(t, os.Symlink(f.vault, filepath.Join(f.home, "settings")))
	c, o, e := f.run("snapshot")
	requireCode(t, 2, c, o, e)
	must(t, os.Remove(filepath.Join(f.home, "settings")))
	f.write(filepath.Join(f.home, "settings/file"), "yes")
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.Remove(filepath.Join(f.home, "settings/file")))
	must(t, os.Mkdir(filepath.Join(f.home, "settings/file"), 0700))
	c, o, e = f.run("install", "--force")
	requireCode(t, 2, c, o, e)
}
func TestTransactionRollback(t *testing.T) {
	root := t.TempDir()
	root, e := filepath.EvalSymlinks(root)
	must(t, e)
	file := filepath.Join(root, "one")
	must(t, os.WriteFile(file, []byte("before"), 0600))
	d := bytesEntry([]byte("after"))
	bad := filepath.Join(root, "nonempty")
	must(t, os.Mkdir(bad, 0750))
	must(t, os.WriteFile(filepath.Join(bad, "child"), []byte("child"), 0600))
	e = apply([]action{{file, &d, []byte("after")}, {path: bad}})
	if e == nil {
		t.Fatal("expected failure")
	}
	b, err := os.ReadFile(file)
	must(t, err)
	if string(b) != "before" {
		t.Fatal("failed rollback")
	}
	info, err := os.Stat(bad)
	must(t, err)
	if info.Mode().Perm() != 0750 {
		t.Fatal("mode rollback")
	}
}
func TestConcurrentLock(t *testing.T) {
	f := setup(t)
	state, e := StateDir(f.vault)
	must(t, e)
	unlock, e := lock(state)
	must(t, e)
	defer unlock()
	c, o, er := f.run("snapshot")
	requireCode(t, 2, c, o, er)
}
func TestDiffTextAndBinary(t *testing.T) {
	f := setup(t, "~/file")
	file := filepath.Join(f.home, "file")
	f.write(file, "before\n")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	f.write(file, "after\n")
	c, o, e = f.run("snapshot", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "-before") || !strings.Contains(o, "+after") {
		t.Fatal(o)
	}
	f.write(file, "\x00after")
	c, o, e = f.run("snapshot", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "binary contents differ") {
		t.Fatal(o)
	}
}
func TestNoExternalCommands(t *testing.T) {
	f := setup(t, "~/file")
	f.write(filepath.Join(f.home, "file"), "yes")
	t.Setenv("PATH", t.TempDir())
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("verify")
	requireCode(t, 0, c, o, e)
}
func TestPOSIXModes(t *testing.T) {
	for _, mode := range []uint32{0600, 0750, 01777, 02750, 04700} {
		if got := posixMode(fileMode(mode)); got != mode {
			t.Fatalf("mode %o becomes %o", mode, got)
		}
	}
}

func TestConfigSymlinkAndIdempotence(t *testing.T) {
	f := setup(t)
	link := filepath.Join(filepath.Dir(f.config), "config-link.toml")
	must(t, os.Symlink(f.config, link))
	f.config = link
	file := filepath.Join(f.home, "file")
	f.write(file, "data")
	c, o, e := f.run("track", file)
	requireCode(t, 0, c, o, e)
	before, err := os.ReadFile(link)
	must(t, err)
	info, err := os.Stat(link)
	must(t, err)
	c, o, e = f.run("track", file)
	requireCode(t, 0, c, o, e)
	after, err := os.ReadFile(link)
	must(t, err)
	now, err := os.Stat(link)
	must(t, err)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(now.ModTime()) {
		t.Fatal("repeat track changed config")
	}
	linfo, err := os.Lstat(link)
	must(t, err)
	if linfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced config symlink")
	}
	c, o, e = f.run("forget", file)
	requireCode(t, 0, c, o, e)
	before, err = os.ReadFile(link)
	must(t, err)
	c, o, e = f.run("forget", file)
	requireCode(t, 0, c, o, e)
	after, err = os.ReadFile(link)
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("repeat forget changed config")
	}
}
func TestDirectoryTrackRetainsExclusions(t *testing.T) {
	f := setup(t, "!~/settings/private/")
	f.write(filepath.Join(f.home, "settings/public"), "yes")
	f.write(filepath.Join(f.home, "settings/private/file"), "no")
	c, o, e := f.run("track", filepath.Join(f.home, "settings"))
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "settings/private/file")) {
		t.Fatal("track overrode exclusion")
	}
}
func TestQuotedAndDottedTOMLPolicy(t *testing.T) {
	for _, policy := range []string{`["targets"]` + "\n" + `"patterns" = ['one', # inline note` + "\n" + `"""two""",]`, `targets.patterns = ['one', "two"]`} {
		t.Run(policy, func(t *testing.T) {
			f := setup(t)
			raw := []byte("# retain\nvault = " + quote(f.vault) + "\n" + policy + "\n")
			f.write(f.config, string(raw))
			f.write(filepath.Join(f.home, "three"), "data")
			c, o, e := f.run("track", filepath.Join(f.home, "three"))
			requireCode(t, 0, c, o, e)
			b, err := os.ReadFile(f.config)
			must(t, err)
			if !strings.Contains(string(b), "# retain") {
				t.Fatal("lost comments")
			}
			if strings.Contains(policy, "inline note") && !strings.Contains(string(b), "inline note") {
				t.Fatal("lost inline note")
			}
		})
	}
}
func TestIncomingDeletionRequiresDeliberateRemoval(t *testing.T) {
	f := setup(t, "~/settings/")
	file := filepath.Join(f.home, "settings/file")
	f.write(file, "one")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.Remove(filepath.Join(f.vault, "settings/file")))
	c, o, e = f.run("install", "--force")
	requireCode(t, 0, c, o, e)
	if !exists(file) {
		t.Fatal("incoming deletion removed installed copy")
	}
	if !strings.Contains(o, "manual installed-copy removal: "+file) {
		t.Fatal("incoming deletion was not reported: " + o)
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 2, c, o, e)
	must(t, os.Remove(file))
	c, o, e = f.run("install")
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
}

func TestScopedTrackDoesNotInspectUnrelatedSelection(t *testing.T) {
	f := setup(t, "~/other/")
	f.write(filepath.Join(f.home, "other/file"), "before")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.RemoveAll(filepath.Join(f.home, "other")))
	must(t, os.Symlink(f.vault, filepath.Join(f.home, "other")))
	f.write(filepath.Join(f.home, "new"), "new")
	c, o, e = f.run("track", filepath.Join(f.home, "new"))
	requireCode(t, 0, c, o, e)
	b, err := os.ReadFile(filepath.Join(f.vault, "other/file"))
	must(t, err)
	if string(b) != "before" {
		t.Fatal("scoped track changed unrelated capture")
	}
}

func TestTrailingSpacesAndLiteralBracketClasses(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"~/name ", "name", true}, {"~/name ", "name ", false}, {`~/name\ `, "name ", true},
		{"~/[]a]", "]", true}, {"~/[]a]", "a", true}, {"~/[!]]", "]", false}, {"~/[!]]", "a", true},
		{"~/[[:digit:]]", "d]", true},
	}
	for _, v := range cases {
		r, e := compile(v.pattern, "/home/person", "/vault")
		must(t, e)
		if got := r.matches(v.name, false); got != v.want {
			t.Fatalf("%q %q got %v want %v", v.pattern, v.name, got, v.want)
		}
	}
}

func TestTrackMissingPolicyArray(t *testing.T) {
	for _, suffix := range []string{"", "[targets] # selection table\n"} {
		t.Run(suffix, func(t *testing.T) {
			f := setup(t)
			f.write(f.config, "vault = "+quote(f.vault)+"\n"+suffix)
			f.write(filepath.Join(f.home, "file"), "data")
			c, o, e := f.run("track", filepath.Join(f.home, "file"))
			requireCode(t, 0, c, o, e)
			c, o, e = f.run("verify")
			requireCode(t, 0, c, o, e)
		})
	}
}

func TestTopLevelWildcardStaysInNamespace(t *testing.T) {
	f := setup(t, "~/*.txt")
	f.write(filepath.Join(f.home, "selected.txt"), "selected")
	unrelated := filepath.Join(f.home, "unrelated-directory")
	must(t, os.Mkdir(unrelated, 0000))
	t.Cleanup(func() { _ = os.Chmod(unrelated, 0700) })
	sibling := filepath.Join(filepath.Dir(f.home), "other-user")
	must(t, os.Mkdir(sibling, 0700))
	f.write(filepath.Join(sibling, "outside.txt"), "outside")
	must(t, os.Chmod(sibling, 0000))
	t.Cleanup(func() { _ = os.Chmod(sibling, 0700) })
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	if !exists(filepath.Join(f.vault, "selected.txt")) {
		t.Fatal("missing top-level match")
	}
	state, err := StateDir(f.vault)
	must(t, err)
	baseline, err := readRecord(filepath.Join(state, "baseline.json"))
	must(t, err)
	if len(baseline.Entries) != 1 {
		t.Fatalf("captured outside namespace: %v", baseline.Entries)
	}
}
func TestNamespaceRootHelpers(t *testing.T) {
	if !within("/home/person/vault", "/") || !within("/", "/") || within("relative", "/") {
		t.Fatal("root ancestor guard")
	}
	c := Config{home: "/home/person"}
	if got := c.name(c.home); got != "home/" {
		t.Fatalf("home namespace root: %q", got)
	}
	if _, e := compile("/", "/home/person", "/vault"); e == nil {
		t.Fatal("selected filesystem root")
	}
	if _, e := compile("~/", "/home/person", "/vault"); e == nil {
		t.Fatal("selected home root")
	}
}

func TestTrackingCommandNames(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"--help"}, &out, &errOut)
	requireCode(t, 0, code, out.String(), errOut.String())
	if !strings.Contains(out.String(), "track PATH") || !strings.Contains(out.String(), "forget PATH") || strings.Contains(out.String(), "add PATH") || strings.Contains(out.String(), "del PATH") {
		t.Fatal("help does not describe the current commands")
	}
	for _, old := range []string{"add", "del"} {
		out.Reset()
		errOut.Reset()
		code = Run([]string{old, "unused"}, &out, &errOut)
		requireCode(t, 2, code, out.String(), errOut.String())
		if !strings.Contains(errOut.String(), "unknown command: "+old) {
			t.Fatalf("legacy command %s was accepted", old)
		}
	}
}

func TestRemovedCommandsAndInvalidOptions(t *testing.T) {
	f := setup(t)
	for _, args := range [][]string{
		{"diff"}, {"diff", "~/file"}, {"snapshot", "--json"}, {"install", "--ask"}, {"install", "--review"}, {"snapshot", "-v"},
		{"snapshot", "--force"}, {"install", "--dry", "--force"}, {"verify", "--diff"}, {"track", "~/file", "--diff"},
		{"install", "--color=never"}, {"snapshot", "--diff", "--color=wrong"}, {"install", "~/file", "--diff"},
	} {
		c, o, e := f.run(args...)
		requireCode(t, 2, c, o, e)
	}
}
