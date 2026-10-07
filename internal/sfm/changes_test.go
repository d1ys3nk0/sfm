package sfm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readText(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	must(t, e)
	return string(b)
}

func TestInstallPreviewAllChanges(t *testing.T) {
	f := setup(t, "~/new", "~/changed", "~/same")
	for _, n := range []string{"new", "changed", "same"} {
		f.write(filepath.Join(f.home, n), "saved\n")
	}
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	must(t, os.Remove(filepath.Join(f.home, "new")))
	f.write(filepath.Join(f.home, "changed"), "local\n")
	must(t, os.RemoveAll(f.state))
	for _, content := range []bool{false, true} {
		args := []string{"install", "--dry"}
		if content {
			args = append(args, "--diff")
		}
		c, o, e = f.run(args...)
		requireCode(t, 0, c, o, e)
		if !strings.Contains(o, "create: "+filepath.Join(f.home, "new")) || !strings.Contains(o, "replace: "+filepath.Join(f.home, "changed")) || strings.Contains(o, filepath.Join(f.home, "same")) || strings.Contains(o, "[y/n]") {
			t.Fatal(o)
		}
		if strings.Contains(o, "@@") != content {
			t.Fatal(o)
		}
		if content && (!strings.Contains(o, "-local\n+saved\n") || !strings.Contains(o, "--- /dev/null") || !strings.Contains(o, "+saved")) {
			t.Fatal(o)
		}
		if exists(filepath.Join(f.home, "new")) || exists(f.state) || readText(t, filepath.Join(f.home, "changed")) != "local\n" || exists(filepath.Join(f.vault, ".sfm.json")) {
			t.Fatal("preview mutated filesystem")
		}
	}
}

func TestInstallApprovalAndDecline(t *testing.T) {
	f := setup(t, "~/a", "~/b", "~/new", "~/same")
	for _, n := range []string{"a", "b", "new", "same"} {
		f.write(filepath.Join(f.home, n), "saved\n")
	}
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	for _, n := range []string{"a", "b"} {
		f.write(filepath.Join(f.vault, n), "incoming\n")
	}
	must(t, os.Remove(filepath.Join(f.home, "new")))
	c, o, e = f.runInput("invalid\nY\nN\n", "install", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "Please enter y or n") || strings.Count(o, "[y/n]") != 3 || strings.Contains(o, "Write "+filepath.Join(f.home, "new")) || strings.Contains(o, "same") {
		t.Fatal(o)
	}
	if readText(t, filepath.Join(f.home, "a")) != "incoming\n" || readText(t, filepath.Join(f.home, "b")) != "saved\n" || !exists(filepath.Join(f.home, "new")) {
		t.Fatal("wrong approval effects")
	}
	c, o, e = f.runInput("y\n", "install")
	requireCode(t, 0, c, o, e)
	if strings.Count(o, "[y/n]") != 1 || strings.Contains(o, "@@") || readText(t, filepath.Join(f.home, "b")) != "incoming\n" {
		t.Fatal(o)
	}
	c, o, e = f.run("snapshot")
	requireCode(t, 0, c, o, e)
	c, o, e = f.runInput("", "install", "--diff")
	requireCode(t, 0, c, o, e)
	if o != "" {
		t.Fatal("unchanged paths or bookkeeping displayed: " + o)
	}
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

func TestInstallInputFailureAbortsAllWrites(t *testing.T) {
	for _, fail := range []string{"eof", "error", "partial"} {
		t.Run(fail, func(t *testing.T) {
			f := setup(t, "~/a", "~/b", "~/new")
			for _, n := range []string{"a", "b", "new"} {
				f.write(filepath.Join(f.home, n), "saved")
			}
			c, o, e := f.run("snapshot")
			requireCode(t, 0, c, o, e)
			for _, n := range []string{"a", "b"} {
				f.write(filepath.Join(f.home, n), "local")
			}
			must(t, os.Remove(filepath.Join(f.home, "new")))
			var input io.Reader = strings.NewReader("")
			if fail == "error" {
				input = readFunc(func([]byte) (int, error) { return 0, errors.New("broken input") })
			}
			if fail == "partial" {
				input = strings.NewReader("y\n")
			}
			var out bytes.Buffer
			_, err := runWithInput([]string{"--config", f.config, "install"}, input, &out)
			if err == nil || !strings.Contains(err.Error(), "approval input") {
				t.Fatal(err)
			}
			if exists(filepath.Join(f.home, "new")) || readText(t, filepath.Join(f.home, "a")) != "local" || readText(t, filepath.Join(f.home, "b")) != "local" {
				t.Fatal("input failure applied partial changes")
			}
		})
	}
}

func TestInstallRevalidatesAfterInput(t *testing.T) {
	for _, target := range []string{"destination", "new destination", "source", "unchanged source", "new payload", "config"} {
		t.Run(target, func(t *testing.T) {
			f := setup(t, "~/file", "~/new", "~/same")
			for _, n := range []string{"file", "new", "same"} {
				f.write(filepath.Join(f.home, n), "saved")
			}
			c, o, e := f.run("snapshot")
			requireCode(t, 0, c, o, e)
			f.write(filepath.Join(f.home, "file"), "local")
			must(t, os.Remove(filepath.Join(f.home, "new")))
			paths := map[string]string{"destination": filepath.Join(f.home, "file"), "new destination": filepath.Join(f.home, "new"), "source": filepath.Join(f.vault, "file"), "unchanged source": filepath.Join(f.vault, "same"), "new payload": filepath.Join(f.vault, ".sfm.json"), "config": f.config}
			input := readFunc(func(p []byte) (int, error) { f.write(paths[target], "other writer"); return copy(p, "y\n"), nil })
			var out bytes.Buffer
			_, err := runWithInput([]string{"--config", f.config, "install"}, input, &out)
			if err == nil || !strings.Contains(err.Error(), "changed during operation") {
				t.Fatalf("%v: %s", err, out.String())
			}
			if readText(t, paths[target]) != "other writer" {
				t.Fatal("concurrent edit overwritten")
			}
			if target != "new destination" && exists(filepath.Join(f.home, "new")) {
				t.Fatal("partial addition")
			}
			if target != "destination" && readText(t, filepath.Join(f.home, "file")) != "local" {
				t.Fatal("partial replacement")
			}
		})
	}
}

func TestForceInstallAndTypeConflicts(t *testing.T) {
	f := setup(t, "~/file")
	file := filepath.Join(f.home, "file")
	f.write(file, "saved\n")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	f.write(file, "local\n")
	c, o, e = f.runInput("", "install", "--force", "--diff")
	requireCode(t, 0, c, o, e)
	if strings.Contains(o, "[y/n]") || !strings.Contains(o, "-local\n+saved\n") || readText(t, file) != "saved\n" {
		t.Fatal(o)
	}
	must(t, os.Remove(file))
	must(t, os.Mkdir(file, 0700))
	for _, args := range [][]string{{"install"}, {"install", "--dry", "--diff"}, {"install", "--force"}} {
		c, o, e = f.run(args...)
		requireCode(t, 2, c, o, e)
		if !strings.Contains(e, "file-type conflict") {
			t.Fatal(e)
		}
	}
}

func TestSnapshotDiffAutomaticAndDeletion(t *testing.T) {
	f := setup(t, "~/dir/")
	file := filepath.Join(f.home, "dir/file")
	deleted := filepath.Join(f.home, "dir/deleted")
	f.write(file, "before\n")
	f.write(deleted, "gone\n")
	c, o, e := f.run("snapshot")
	requireCode(t, 0, c, o, e)
	f.write(file, "after")
	must(t, os.Remove(deleted))
	c, o, e = f.run("snapshot", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	for _, want := range []string{"-before\n+after\n", "\\ No newline at end of file", "+++ /dev/null", "-gone"} {
		if !strings.Contains(o, want) {
			t.Fatal(o)
		}
	}
	if readText(t, filepath.Join(f.vault, "dir/file")) != "before\n" || !exists(filepath.Join(f.vault, "dir/deleted")) {
		t.Fatal("preview wrote")
	}
	c, o, e = f.runInput("", "snapshot", "--diff")
	requireCode(t, 0, c, o, e)
	if strings.Contains(o, "[y/n]") || readText(t, filepath.Join(f.vault, "dir/file")) != "after" || exists(filepath.Join(f.vault, "dir/deleted")) {
		t.Fatal(o)
	}
}

func TestDiffEmptyBinaryModesAndLinks(t *testing.T) {
	f := setup(t, "~/empty", "~/binary", "~/mode", "~/link")
	f.write(filepath.Join(f.home, "empty"), "")
	f.write(filepath.Join(f.home, "binary"), "\x00data")
	f.write(filepath.Join(f.home, "mode"), "same\n")
	must(t, os.Symlink("first", filepath.Join(f.home, "link")))
	c, o, e := f.run("snapshot", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "empty file") || !strings.Contains(o, "binary contents differ") || !strings.Contains(o, "link /dev/null -> first") {
		t.Fatal(o)
	}
	must(t, os.Chmod(filepath.Join(f.home, "mode"), 0750))
	must(t, os.Remove(filepath.Join(f.home, "link")))
	must(t, os.Symlink("second", filepath.Join(f.home, "link")))
	c, o, e = f.run("snapshot", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "[0600 -> 0750]") || !strings.Contains(o, "link first -> second") || strings.Contains(o, "@@") {
		t.Fatal(o)
	}
	for _, n := range []string{"empty", "binary"} {
		must(t, os.Remove(filepath.Join(f.home, n)))
	}
	c, o, e = f.run("install", "--dry", "--diff")
	requireCode(t, 0, c, o, e)
	if !strings.Contains(o, "empty file") || !strings.Contains(o, "binary contents differ") || !strings.Contains(o, "[0750 -> 0600]") || !strings.Contains(o, "link second -> first") {
		t.Fatal(o)
	}
}

func TestInstallIgnoresLegacyHistoryChangesDuringInput(t *testing.T) {
	f := setup(t, "~/file")
	f.write(filepath.Join(f.home, "file"), "local")
	f.write(filepath.Join(f.vault, "file"), "incoming")
	state, err := StateDir(f.vault)
	must(t, err)
	baseline := filepath.Join(state, "baseline.json")
	f.write(baseline, "old history")
	input := readFunc(func(p []byte) (int, error) {
		f.write(baseline, "other writer")
		return copy(p, "y\n"), nil
	})
	var out bytes.Buffer
	_, err = runWithInput([]string{"--config", f.config, "install"}, input, &out)
	must(t, err)
	if readText(t, filepath.Join(f.home, "file")) != "incoming" || readText(t, baseline) != "other writer" {
		t.Fatal("legacy history affected installation")
	}
}

type writeFunc func([]byte) (int, error)

func (f writeFunc) Write(p []byte) (int, error) { return f(p) }

func TestCaptureAndForgetRevalidateBeforeWrites(t *testing.T) {
	for _, cmd := range []string{"snapshot", "forget"} {
		for _, target := range []string{"local", "vault", "config", "new payload"} {
			t.Run(cmd+"/"+target, func(t *testing.T) {
				f := setup(t, "~/file")
				local := filepath.Join(f.home, "file")
				vault := filepath.Join(f.vault, "file")
				f.write(local, "local")
				f.write(vault, "before")
				configBefore := readText(t, f.config)
				paths := map[string]string{"local": local, "vault": vault, "config": f.config, "new payload": filepath.Join(f.vault, "new")}
				changed := false
				output := writeFunc(func(p []byte) (int, error) {
					if !changed {
						f.write(paths[target], "other writer")
						changed = true
					}
					return len(p), nil
				})
				args := []string{"--config", f.config, cmd}
				if cmd == "forget" {
					args = append(args, local)
				}
				_, err := runWithInput(args, strings.NewReader(""), output)
				if err == nil || !strings.Contains(err.Error(), "changed during") {
					t.Fatal(err)
				}
				if readText(t, paths[target]) != "other writer" {
					t.Fatal("concurrent edit overwritten")
				}
				if target != "vault" && readText(t, vault) != "before" {
					t.Fatal("partial vault change")
				}
				if target != "config" && readText(t, f.config) != configBefore {
					t.Fatal("partial configuration change")
				}
			})
		}
	}
}
