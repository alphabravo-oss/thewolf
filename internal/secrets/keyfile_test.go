package secrets

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestKeyFileConcurrentProcesses(t *testing.T) {
	if path := os.Getenv("WOLF_KEYFILE_CHILD"); path != "" {
		key, err := LoadOrCreateKeyFile(path, func() ([]byte, error) {
			fmt.Println("ready")
			if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
				return nil, err
			}
			key := make([]byte, 32)
			_, err := rand.Read(key)
			return key, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%x\n", sha256.Sum256(key))
		return
	}
	path := filepath.Join(t.TempDir(), "keys", "test.key")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type child struct {
		cmd    *exec.Cmd
		input  io.WriteCloser
		output *bufio.Reader
		stderr *bytes.Buffer
	}
	var children []child
	t.Cleanup(func() {
		for _, child := range children {
			_ = child.cmd.Process.Kill()
			_ = child.cmd.Wait()
		}
	})
	for range 8 {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestKeyFileConcurrentProcesses$")
		cmd.Env = append(os.Environ(), "WOLF_KEYFILE_CHILD="+path)
		stderr := new(bytes.Buffer)
		cmd.Stderr = stderr
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(output)
		children = append(children, child{cmd, input, reader, stderr})
		if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
			t.Fatalf("child did not reach the generation barrier: %q, %v", line, err)
		}
	}
	// Every process has observed an absent file and generated its own candidate.
	for _, child := range children {
		if _, err := child.input.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		_ = child.input.Close()
	}
	var hashes []string
	for _, child := range children {
		hash, err := child.output.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, child.output); err != nil {
			t.Fatal(err)
		}
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("key initialization failed: %v: %s", err, child.stderr.String())
		}
		hashes = append(hashes, hash)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x\n", sha256.Sum256(data))
	for _, hash := range hashes {
		if hash != want {
			t.Fatal("a process returned a key other than the persisted winner")
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 32 {
		t.Fatalf("unexpected persisted key mode/size: %v, %v", info, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary key entries were not removed: %v, %v", entries, err)
	}
}

func TestKeyFilePreservesExistingEntriesAndPinsDirectory(t *testing.T) {
	for _, kind := range []string{"existing", "directory", "escaping-link", "dangling-link"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			path, target := filepath.Join(root, "test.key"), filepath.Join(outside, "target")
			original := []byte("existing key bytes")
			if kind == "existing" {
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "escaping-link" {
					if err := os.WriteFile(target, original, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			generated := false
			data, err := LoadOrCreateKeyFile(path, func() ([]byte, error) {
				generated = true
				return []byte("replacement"), nil
			})
			if kind == "existing" {
				if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("existing key not reused: %v", err)
				}
			} else if err == nil {
				t.Fatal("unreadable/escaping key must fail")
			}
			if generated {
				t.Fatal("existing entry triggered key generation")
			}
			if kind == "escaping-link" {
				data, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatal("outside key changed")
				}
			} else if kind == "dangling-link" {
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("dangling link target created")
				}
			}
		})
	}

	root, outside := t.TempDir(), t.TempDir()
	held := root + "-held"
	t.Cleanup(func() { _ = os.RemoveAll(held) })
	want := []byte("key in the originally opened directory")
	data, err := LoadOrCreateKeyFile(filepath.Join(root, "test.key"), func() ([]byte, error) {
		if err := os.Rename(root, held); err != nil {
			return nil, err
		}
		if err := os.Symlink(outside, root); err != nil {
			return nil, err
		}
		return want, nil
	})
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("renamed directory initialization failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(held, "test.key")); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("key was not published in opened root: %v", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("replacement directory changed: %v, %v", entries, err)
	}
}

func TestKeyFileLoadsWinnerCreatedDuringGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.key")
	winner := []byte("the other process's persisted key")
	data, err := LoadOrCreateKeyFile(path, func() ([]byte, error) {
		if err := os.WriteFile(path, winner, 0600); err != nil {
			return nil, err
		}
		return []byte("losing candidate must not be published"), nil
	})
	if err != nil || !bytes.Equal(data, winner) {
		t.Fatalf("did not load the concurrent winner: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, winner) {
		t.Fatal("overwrote the concurrently created key")
	}
}
