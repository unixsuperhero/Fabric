package removal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAllDeletesOnlyAfterApproval(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	var asked []string
	declining := Remover{Confirm: func(path string) bool { asked = append(asked, path); return false }}
	if err := declining.RemoveAll(target); !errors.Is(err, ErrDeclined) {
		t.Fatalf("declined removal error = %v, want ErrDeclined", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("declined removal deleted the directory: %v", err)
	}
	if len(asked) != 1 || asked[0] != target {
		t.Errorf("confirmer asked about %q, want [%q]", asked, target)
	}

	if err := Always.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("approved removal left the directory: %v", err)
	}
}

func TestRemoveDeletesFileAfterApproval(t *testing.T) {
	file := filepath.Join(t.TempDir(), "attachment.png")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Never.Remove(file); !errors.Is(err, ErrDeclined) {
		t.Fatalf("error = %v, want ErrDeclined", err)
	}
	if err := Always.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
}

func TestProtectedPathsAreNeverRemoved(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"empty":        "",
		"blank":        "   ",
		"dot":          ".",
		"cwd":          cwd,
		"cwd parent":   filepath.Dir(cwd),
		"cwd ancestor": filepath.Dir(filepath.Dir(cwd)),
		"home":         home,
		"root":         string(filepath.Separator),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			r := Remover{Confirm: func(string) bool { called = true; return true }}
			if err := r.RemoveAll(path); !errors.Is(err, ErrProtected) {
				t.Fatalf("RemoveAll(%q) error = %v, want ErrProtected", path, err)
			}
			if err := r.Remove(path); !errors.Is(err, ErrProtected) {
				t.Fatalf("Remove(%q) error = %v, want ErrProtected", path, err)
			}
			if called {
				t.Error("confirmer was consulted for a protected path")
			}
			if _, err := os.Stat(cwd); err != nil {
				t.Fatalf("working directory is gone: %v", err)
			}
		})
	}
}

func TestChildOfWorkingDirectoryIsRemovable(t *testing.T) {
	t.Chdir(t.TempDir())
	child := "child"
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Always.RemoveAll(child); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Errorf("child directory still exists: %v", err)
	}
}

func TestNilConfirmerDeclines(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Remover{}).Remove(file); !errors.Is(err, ErrDeclined) {
		t.Fatalf("error = %v, want ErrDeclined", err)
	}
}

type fakeTerminal struct {
	io.Reader
	out bytes.Buffer
}

func (f *fakeTerminal) Write(p []byte) (int, error) { return f.out.Write(p) }

func TestAskAcceptsOnlyExplicitYes(t *testing.T) {
	cases := map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, " YES \n": true,
		"\n": false, "n\n": false, "no\n": false, "yolo\n": false, "": false,
	}
	for input, want := range cases {
		term := &fakeTerminal{Reader: bytes.NewBufferString(input)}
		if got := ask(term, "/tmp/x"); got != want {
			t.Errorf("ask(%q) = %v, want %v", input, got, want)
		}
		if !bytes.Contains(term.out.Bytes(), []byte("remove /tmp/x? [y/N]")) {
			t.Errorf("prompt for %q = %q", input, term.out.String())
		}
	}
}
