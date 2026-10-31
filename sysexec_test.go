package sysproxy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// useToolDirs makes dirs the system tool directories and sets the privilege
// level for one test.
func useToolDirs(t *testing.T, root bool, dirs ...string) {
	t.Helper()
	origDirs, origPriv := systemToolDirs, privileged
	systemToolDirs = func() []string { return dirs }
	privileged = func() bool { return root }
	t.Cleanup(func() { systemToolDirs, privileged = origDirs, origPriv })
}

// writeTool installs a shell script called name in dir.
func writeTool(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil { //nolint:gosec // test helper
		t.Fatal(err)
	}
	return path
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script tools")
	}
}

func TestLookSystemTool_SystemDirectoryBeatsPATH(t *testing.T) {
	skipOnWindows(t)
	system, evil := t.TempDir(), t.TempDir()
	want := writeTool(t, system, "mytool", "echo trusted")
	writeTool(t, evil, "mytool", "echo hijacked")
	t.Setenv("PATH", evil)

	for _, root := range []bool{false, true} {
		useToolDirs(t, root, system)
		got, err := lookSystemTool("mytool")
		if err != nil || got != want {
			t.Errorf("root=%v: lookSystemTool = %q, %v; want %q", root, got, err, want)
		}
	}
}

func TestLookSystemTool_PATHFallbackOnlyWithoutPrivileges(t *testing.T) {
	skipOnWindows(t)
	empty, onPath := t.TempDir(), t.TempDir()
	want := writeTool(t, onPath, "mytool", "echo from-path")
	t.Setenv("PATH", onPath)

	useToolDirs(t, false, empty)
	if got, err := lookSystemTool("mytool"); err != nil || got != want {
		t.Errorf("unprivileged: lookSystemTool = %q, %v; want the PATH tool %q", got, err, want)
	}

	useToolDirs(t, true, empty)
	got, err := lookSystemTool("mytool")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("privileged: lookSystemTool = %q, %v; want exec.ErrNotFound", got, err)
	}
	if !errors.Is(execError("mytool", "run", err, nil), ErrToolMissing) {
		t.Errorf("a missing tool must surface as ErrToolMissing")
	}
	if hasSystemTool("mytool") {
		t.Errorf("hasSystemTool found a PATH tool for a privileged process")
	}
}

func TestLookSystemTool_IgnoresDirectoriesAndNonExecutables(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("data"), 0o644); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	useToolDirs(t, true, dir)
	for _, name := range []string{"adir", "plain", "absent"} {
		if _, err := lookSystemTool(name); !errors.Is(err, exec.ErrNotFound) {
			t.Errorf("%s: err = %v, want exec.ErrNotFound", name, err)
		}
	}
}

// The attack of S1: a writable directory early in PATH holds a fake tool that
// would receive the arguments (proxy credentials among them).
func TestSystemCommand_RunsTheSystemToolNotTheOneInPATH(t *testing.T) {
	skipOnWindows(t)
	system, evil := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "stolen")
	writeTool(t, system, "mytool", `echo "trusted $*"`)
	writeTool(t, evil, "mytool", `echo "$*" > `+sentinel+`; echo hijacked`)
	t.Setenv("PATH", evil+string(os.PathListSeparator)+os.Getenv("PATH"))
	useToolDirs(t, false, system)

	out, err := systemCommand(context.Background(), "mytool", "user:s3cr3t").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "trusted user:s3cr3t" {
		t.Errorf("output = %q, want the system tool's", got)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("the tool in PATH ran and received the arguments")
	}
}

func TestSystemCommand_MissingToolFailsToStart(t *testing.T) {
	skipOnWindows(t)
	useToolDirs(t, true, t.TempDir())
	t.Setenv("PATH", os.Getenv("PATH")) // present, but must not be consulted

	err := systemCommand(context.Background(), "sh", "-c", "echo should-not-run").Run()
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Run() = %v, want exec.ErrNotFound", err)
	}
}

func TestSystemCommand_HonoursContextCancellation(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	writeTool(t, dir, "slow", "sleep 5")
	useToolDirs(t, true, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := systemCommand(ctx, "slow").Run(); err == nil {
		t.Fatal("a canceled context must stop the command")
	}
}

func TestPlatformToolDirs_AreAbsoluteAndSystemOwned(t *testing.T) {
	dirs := platformToolDirs()
	if len(dirs) == 0 {
		t.Fatal("no system tool directories")
	}
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			t.Errorf("%q is not absolute", d)
		}
		if strings.Contains(filepath.ToSlash(d), "/usr/local/") {
			t.Errorf("%q is user-writable on common systems and must not be trusted", d)
		}
	}
}
