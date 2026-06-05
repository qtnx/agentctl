package state

import (
	"os"
	"testing"
	"time"
)

func TestStoreSaveLoadListDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	task := Task{
		TaskID:    "XL-123",
		TokenID:   "42",
		CreatedAt: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
	}

	if err := store.Save(task); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dir + "/tasks/XL-123.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("saved file mode = %o, want %o", got, 0600)
	}

	loaded, err := store.Load("XL-123")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TaskID != "XL-123" {
		t.Fatalf("task id = %q", loaded.TaskID)
	}
	if loaded.TokenID != "42" {
		t.Fatalf("token id = %q", loaded.TokenID)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want %d", len(tasks), 1)
	}

	if err := store.Delete("XL-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("XL-123"); err == nil {
		t.Fatal("expected load after delete to fail")
	}
}
