package sfm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryVaultMapping(t *testing.T) {
	c := Config{Vault: "/synthetic/vault", home: "/synthetic/home"}
	for _, tc := range []struct{ name, path, destination string }{
		{"home/.config/app/file", "/synthetic/vault/.config/app/file", "/synthetic/home/.config/app/file"},
		{"home/root/file", "/synthetic/vault/root/file", "/synthetic/home/root/file"},
		{"root/etc/app/file", "/synthetic/vault/_/etc/app/file", "/etc/app/file"},
	} {
		if got := c.vaultPath(tc.name); got != tc.path {
			t.Fatalf("mapping %s: %s", tc.name, got)
		}
		name, e := c.vaultName(tc.path)
		must(t, e)
		if name != tc.name {
			t.Fatalf("inverse %s: %s", tc.path, name)
		}
		destination, e := c.destination(name)
		must(t, e)
		if destination != tc.destination {
			t.Fatalf("destination %s: %s", name, destination)
		}
	}
	for _, name := range []string{"home/_", "home/_/file", "home/..", "root/", "root/synthetic/home", "root/synthetic/home/file", "home/a\nb"} {
		if _, e := c.destination(name); e == nil {
			t.Fatal("accepted unsafe identifier " + name)
		}
	}
	for _, structural := range []string{c.Vault, filepath.Join(c.Vault, "_")} {
		name, e := c.vaultName(structural)
		must(t, e)
		if name != "" {
			t.Fatal(name)
		}
	}
}

func TestReservedHomeTargets(t *testing.T) {
	f := setup(t)
	for _, target := range []string{"~/_", "~/_/file", `~/\_`, f.home, f.home + "/file"} {
		if _, e := compile(target, f.home, f.vault); e == nil {
			t.Fatal("accepted reserved/aliased target " + target)
		}
	}
	f.write(filepath.Join(f.home, "_/file"), "reserved")
	f.write(f.config, "vault = "+quote(f.vault)+"\n[targets]\npatterns = [\"~/*\"]\n")
	c, o, e := f.run("snapshot", "--dry")
	requireCode(t, 2, c, o, e)
	if !strings.Contains(e, "reserved home target") {
		t.Fatal(e)
	}
	for _, cmd := range []string{"track", "forget"} {
		c, o, e = f.run(cmd, filepath.Join(f.home, "_"))
		requireCode(t, 2, c, o, e)
	}
}

func TestVaultStructuralDirectory(t *testing.T) {
	for _, kind := range []string{"file", "link", "dir"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			path := filepath.Join(f.vault, "_")
			switch kind {
			case "file":
				f.write(path, "not structural")
			case "link":
				must(t, os.Symlink(f.home, path))
			case "dir":
				must(t, os.Mkdir(path, 0750))
			}
			c, o, e := f.run("verify")
			if kind == "dir" {
				requireCode(t, 0, c, o, e)
				c, o, e = f.run("install", "--dry")
				requireCode(t, 0, c, o, e)
				if o != "" {
					t.Fatal("structural directory installed: " + o)
				}
			} else {
				requireCode(t, 2, c, o, e)
				if !strings.Contains(e, "real directory") {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestAllVaultFilesArePayloads(t *testing.T) {
	f := setup(t)
	for _, n := range []string{".sfm.json", ".git/marker", "home/file", "root/file", "unselected"} {
		f.write(filepath.Join(f.vault, n), "payload\n")
	}
	c, o, e := f.run("verify")
	requireCode(t, 0, c, o, e)
	c, o, e = f.run("install", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	for _, n := range []string{".sfm.json", ".git/marker", "home/file", "root/file", "unselected"} {
		if !strings.Contains(o, "create "+filepath.Join(f.home, n)) {
			t.Fatal("omitted payload: " + n + "\n" + o)
		}
	}
	if exists(f.state) {
		t.Fatal("preview created state")
	}
	c, o, e = f.run("install", "--force")
	requireCode(t, 0, c, o, e)
	for _, n := range []string{".sfm.json", ".git/marker", "home/file", "root/file", "unselected"} {
		if readText(t, filepath.Join(f.home, n)) != "payload\n" {
			t.Fatal(n)
		}
	}
}

func TestSnapshotLayoutAndActualModes(t *testing.T) {
	f := setup(t, "~/dir/")
	file := filepath.Join(f.home, "dir/file")
	f.write(file, "home payload")
	must(t, os.Mkdir(filepath.Join(f.home, "dir/empty"), 0750))
	must(t, os.Symlink("file", filepath.Join(f.home, "dir/link")))
	rootFile := filepath.Join(filepath.Dir(f.home), "root-file")
	f.write(rootFile, "root payload")
	raw := readText(t, f.config)
	f.write(f.config, strings.Replace(raw, "patterns = [", "patterns = [\n"+quote(rootFile)+",", 1))
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	rootPayload := filepath.Join(f.vault, "_", strings.TrimPrefix(rootFile, "/"))
	if readText(t, rootPayload) != "root payload" || readText(t, filepath.Join(f.vault, "dir/file")) != "home payload" || exists(filepath.Join(f.vault, ".sfm.json")) || exists(filepath.Join(f.vault, "home")) {
		t.Fatal("wrong layout")
	}
	if !strings.Contains(o, "create dir/file") || !strings.Contains(o, "create _/"+strings.TrimPrefix(rootFile, "/")) || strings.Contains(o, "create home/") {
		t.Fatal(o)
	}
	for _, name := range []string{"empty", "link"} {
		want, err := entry(filepath.Join(f.home, "dir", name))
		must(t, err)
		got, err := entry(filepath.Join(f.vault, "dir", name))
		must(t, err)
		if got != want {
			t.Fatalf("entry %s: %v != %v", name, got, want)
		}
	}
	state, err := StateDir(f.vault)
	must(t, err)
	baseline, err := readRecord(filepath.Join(state, "baseline.json"))
	must(t, err)
	cfg, err := readConfig(f.config)
	must(t, err)
	m, err := newManager(cfg, &strings.Builder{})
	must(t, err)
	actual, err := m.vaultEntries()
	must(t, err)
	if len(actual) != len(baseline.Entries) {
		t.Fatalf("baseline omitted created ancestor dirs: %v / %v", baseline.Entries, actual)
	}
	for n, d := range actual {
		if baseline.Entries[n] != d {
			t.Fatal("baseline differs: " + n)
		}
	}
	if strings.Contains(readText(t, filepath.Join(state, "baseline.json")), "metadata") || baseline.Version != 3 {
		t.Fatal("wrong baseline format")
	}
	// Dry preview only: a forced synthetic root install could alter system ancestors.
	must(t, os.Remove(rootFile))
	c, o, e = f.run("install", "--dry")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "create "+rootFile) || exists(rootFile) {
		t.Fatal(o)
	}
}

func TestVaultChmodRequiresReconciliation(t *testing.T) {
	f := setup(t, "~/file")
	f.write(filepath.Join(f.home, "file"), "same")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.Chmod(filepath.Join(f.vault, "file"), 0750))
	c, o, e = f.run("snapshot")
	requireCode(t, 2, c, o, e)
	c, o, e = f.run("install", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "permissions 0600 -> 0750") {
		t.Fatal(o)
	}
	c, o, e = f.runInput("y\n", "install")
	requireCode(t, 0, c, o, e)
	got, err := entry(filepath.Join(f.home, "file"))
	must(t, err)
	if got.Mode != 0750 {
		t.Fatal(got)
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
}

func TestForgetPreservesUnselectedChildren(t *testing.T) {
	f := setup(t, "~/dir/", "!~/dir/retained/")
	f.write(filepath.Join(f.home, "dir/selected"), "selected")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	f.write(filepath.Join(f.vault, "dir/retained/file"), "retained")
	c, o, e = f.run("forget", filepath.Join(f.home, "dir"))
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "dir/selected")) || readText(t, filepath.Join(f.vault, "dir/retained/file")) != "retained" || !exists(filepath.Join(f.home, "dir/selected")) {
		t.Fatal("forgot wrong payloads")
	}
	// Remaining payloads stay installable even though capture rules exclude them.
	c, o, e = f.run("install", "--dry")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "create "+filepath.Join(f.home, "dir/retained/file")) {
		t.Fatal(o)
	}
}

func TestForgetAbsentDirectoryUsesVault(t *testing.T) {
	f := setup(t, "~/dir/")
	f.write(filepath.Join(f.home, "dir/file"), "saved")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.RemoveAll(filepath.Join(f.home, "dir")))
	c, o, e = f.run("forget", filepath.Join(f.home, "dir"))
	requireCode(t, 0, c, o, e)
	if exists(filepath.Join(f.vault, "dir")) {
		t.Fatal("absent directory was not forgotten")
	}
	cfg, err := readConfig(f.config)
	must(t, err)
	if cfg.selected("home/dir/file", false) {
		t.Fatal("absent directory selection retained")
	}
}

func TestOldBaselineRequiresMigration(t *testing.T) {
	f := setup(t)
	state, e := StateDir(f.vault)
	must(t, e)
	path := filepath.Join(state, "baseline.json")
	old := `{"version":2,"entries":{},"metadata":{}}`
	f.write(path, old)
	for _, cmd := range []string{"install", "snapshot"} {
		c, o, errOut := f.run(cmd, "--dry")
		requireCode(t, 2, c, o, errOut)
		if !strings.Contains(errOut, "migrate the vault layout and local baseline to version 3") || readText(t, path) != old {
			t.Fatal(errOut)
		}
	}
	c, o, errOut := f.run("verify")
	requireCode(t, 0, c, o, errOut)
}

func TestVerifyDoesNotInspectSources(t *testing.T) {
	f := setup(t, "~/unreadable/")
	must(t, os.Mkdir(filepath.Join(f.home, "unreadable"), 0000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(f.home, "unreadable"), 0700) })
	f.write(filepath.Join(f.vault, "unselected"), "valid")
	c, o, e := f.run("verify")
	requireCode(t, 0, c, o, e)
	if strings.Contains(o, "WARNING") || exists(f.state) {
		t.Fatal(o)
	}
}

func TestSnapshotKeepsUnselectedBaselinePending(t *testing.T) {
	f := setup(t, "~/chosen")
	f.write(filepath.Join(f.vault, "other"), "old")
	c, o, e := f.run("install")
	requireCode(t, 0, c, o, e)
	f.write(filepath.Join(f.vault, "other"), "incoming")
	f.write(filepath.Join(f.home, "chosen"), "new")
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	state, err := StateDir(f.vault)
	must(t, err)
	base, err := readRecord(filepath.Join(state, "baseline.json"))
	must(t, err)
	if base.Entries["home/other"].Hash != bytesEntry([]byte("old")).Hash || readText(t, filepath.Join(f.vault, "other")) != "incoming" {
		t.Fatal("unselected incoming state was acknowledged")
	}
}
