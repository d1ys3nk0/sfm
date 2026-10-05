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
	saved := filepath.Join(f.vault, "home/settings/config")
	b, err := os.ReadFile(saved)
	must(t, err)
	if string(b) != "first\n" {
		t.Fatal("capture")
	}
	f.write(saved, "incoming\n")
	c, o, e = f.run("snapshot")
	requireCode(t, 2, c, o, e)
	c, o, e = f.run("install")
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
func TestDryRunJSONNoMutation(t *testing.T) {
	f := setup(t, "~/file")
	f.write(filepath.Join(f.home, "file"), "payload")
	c, o, e := f.run("snapshot", "--dry", "--json")
	requireCode(t, 0, c, o, e)
	var p struct{ Entries map[string]*Entry }
	must(t, json.Unmarshal([]byte(o), &p))
	if p.Entries["home/file"] == nil || p.Entries[".sfm.json"] == nil {
		t.Fatal(o)
	}
	if exists(f.state) || exists(filepath.Join(f.vault, "home")) || exists(filepath.Join(f.vault, ".sfm.json")) {
		t.Fatal("dry run wrote files")
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	for n, want := range p.Entries {
		got, err := entry(filepath.Join(f.vault, n))
		must(t, err)
		if got != *want {
			t.Fatalf("plan mismatch %s: %#v %#v", n, got, want)
		}
	}
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
	if exists(filepath.Join(f.vault, "home/settings/private/secret")) || !exists(filepath.Join(f.vault, "home/settings/private/public")) {
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
	if !exists(filepath.Join(f.vault, "home/settings/file")) {
		t.Fatal("missing root deleted capture")
	}
}
func TestTrackAndComments(t *testing.T) {
	f := setup(t)
	f.write(filepath.Join(f.home, "one"), "one")
	f.write(filepath.Join(f.home, "two"), "two")
	c, o, e := f.run("add", filepath.Join(f.home, "one"))
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("add", filepath.Join(f.home, "two"))
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("del", filepath.Join(f.home, "one"))
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "home/one")) || !exists(filepath.Join(f.vault, "home/two")) || !exists(filepath.Join(f.home, "one")) {
		t.Fatal("scope or source mutation")
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
func TestUnsafeMetadata(t *testing.T) {
	f := setup(t)
	f.write(filepath.Join(f.vault, ".sfm.json"), `{"version":2,"entries":{"home/..":{"type":"dir","mode":448}}}`)
	c, o, e := f.run("install")
	requireCode(t, 2, c, o, e)
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
	c, o, e = f.run("diff")
	requireCode(t, 1, c, o, e)
	if !strings.Contains(o, "-before") || !strings.Contains(o, "+after") {
		t.Fatal(o)
	}
	f.write(file, "\x00after")
	c, o, e = f.run("diff")
	requireCode(t, 1, c, o, e)
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
	c, o, e := f.run("add", file)
	requireCode(t, 0, c, o, e)
	before, err := os.ReadFile(link)
	must(t, err)
	info, err := os.Stat(link)
	must(t, err)
	c, o, e = f.run("add", file)
	requireCode(t, 0, c, o, e)
	after, err := os.ReadFile(link)
	must(t, err)
	now, err := os.Stat(link)
	must(t, err)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(now.ModTime()) {
		t.Fatal("repeat add changed config")
	}
	linfo, err := os.Lstat(link)
	must(t, err)
	if linfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced config symlink")
	}
	c, o, e = f.run("del", file)
	requireCode(t, 0, c, o, e)
	before, err = os.ReadFile(link)
	must(t, err)
	c, o, e = f.run("del", file)
	requireCode(t, 0, c, o, e)
	after, err = os.ReadFile(link)
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("repeat delete changed config")
	}
}
func TestDirectoryAddRetainsExclusions(t *testing.T) {
	f := setup(t, "!~/settings/private/")
	f.write(filepath.Join(f.home, "settings/public"), "yes")
	f.write(filepath.Join(f.home, "settings/private/file"), "no")
	c, o, e := f.run("add", filepath.Join(f.home, "settings"))
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "home/settings/private/file")) {
		t.Fatal("add overrode exclusion")
	}
}
func TestQuotedAndDottedTOMLPolicy(t *testing.T) {
	for _, policy := range []string{`["targets"]` + "\n" + `"patterns" = ['one', # inline note` + "\n" + `"""two""",]`, `targets.patterns = ['one', "two"]`} {
		t.Run(policy, func(t *testing.T) {
			f := setup(t)
			raw := []byte("# retain\nvault = " + quote(f.vault) + "\n" + policy + "\n")
			f.write(f.config, string(raw))
			f.write(filepath.Join(f.home, "three"), "data")
			c, o, e := f.run("add", filepath.Join(f.home, "three"))
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
	must(t, os.Remove(filepath.Join(f.vault, "home/settings/file")))
	c, o, e = f.run("install", "--force")
	requireCode(t, 0, c, o, e)
	if !exists(file) {
		t.Fatal("incoming deletion removed installed copy")
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 2, c, o, e)
	must(t, os.Remove(file))
	c, o, e = f.run("install")
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
}

func TestScopedAddDoesNotInspectUnrelatedSelection(t *testing.T) {
	f := setup(t, "~/other/")
	f.write(filepath.Join(f.home, "other/file"), "before")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.RemoveAll(filepath.Join(f.home, "other")))
	must(t, os.Symlink(f.vault, filepath.Join(f.home, "other")))
	f.write(filepath.Join(f.home, "new"), "new")
	c, o, e = f.run("add", filepath.Join(f.home, "new"))
	requireCode(t, 0, c, o, e)
	b, err := os.ReadFile(filepath.Join(f.vault, "home/other/file"))
	must(t, err)
	if string(b) != "before" {
		t.Fatal("scoped add changed unrelated capture")
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

func TestAddMissingPolicyArray(t *testing.T) {
	for _, suffix := range []string{"", "[targets] # selection table\n"} {
		t.Run(suffix, func(t *testing.T) {
			f := setup(t)
			f.write(f.config, "vault = "+quote(f.vault)+"\n"+suffix)
			f.write(filepath.Join(f.home, "file"), "data")
			c, o, e := f.run("add", filepath.Join(f.home, "file"))
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
	if !exists(filepath.Join(f.vault, "home/selected.txt")) {
		t.Fatal("missing top-level match")
	}
	metadata, err := readRecord(filepath.Join(f.vault, ".sfm.json"), false)
	must(t, err)
	if len(metadata.Entries) != 1 {
		t.Fatalf("captured outside namespace: %v", metadata.Entries)
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
