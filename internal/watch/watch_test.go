package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPollCallsOnChangeWhenFileChanges(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "package.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changed := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Poll(ctx, root, 5*time.Millisecond, []string{"node_modules"}, func(context.Context) error {
			changed <- struct{}{}
			cancel()
			return nil
		})
	}()

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(file, []byte(`{"changed":true}`), 0600); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for change callback")
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestPollIgnoresExcludedDirectories(t *testing.T) {
	root := t.TempDir()
	excludedDir := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(excludedDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changed := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Poll(ctx, root, 5*time.Millisecond, []string{"node_modules"}, func(context.Context) error {
			changed <- struct{}{}
			return nil
		})
	}()

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(excludedDir, "cache.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	cancel()

	select {
	case <-changed:
		t.Fatal("change callback fired for excluded directory")
	default:
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}
