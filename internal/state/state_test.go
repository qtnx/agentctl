package state

import (
	"errors"
	"os"
	"path/filepath"
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

	tasksInfo, err := os.Stat(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if got := tasksInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("tasks directory mode = %o, want %o", got, 0700)
	}

	info, err := os.Stat(filepath.Join(dir, "tasks", "XL-123.json"))
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

func TestStoreRejectsInvalidTaskIDs(t *testing.T) {
	absoluteID := filepath.Join(t.TempDir(), "absolute")
	tests := []struct {
		name   string
		taskID string
	}{
		{name: "empty", taskID: ""},
		{name: "parent traversal", taskID: "../other"},
		{name: "slash separator", taskID: "foo/bar"},
		{name: "backslash separator", taskID: "foo\\bar"},
		{name: "absolute path", taskID: absoluteID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(dir)
			unsafePath := seedUnsafeTaskFile(t, dir, tt.taskID)

			checkInvalidTaskIDError(t, "Save", store.Save(Task{TaskID: tt.taskID}))

			_, err := store.Load(tt.taskID)
			checkInvalidTaskIDError(t, "Load", err)

			checkInvalidTaskIDError(t, "Delete", store.Delete(tt.taskID))

			if _, err := os.Stat(unsafePath); err != nil {
				t.Fatalf("unsafe file was modified or removed: %v", err)
			}
		})
	}
}

func seedUnsafeTaskFile(t *testing.T, stateDir, taskID string) string {
	t.Helper()

	path := filepath.Join(stateDir, "tasks", taskID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"task_id":"seed"}`), 0600); err != nil {
		t.Fatal(err)
	}

	return path
}

func checkInvalidTaskIDError(t *testing.T, operation string, err error) {
	t.Helper()

	if err == nil {
		t.Errorf("%s error = nil, want invalid task id error", operation)
		return
	}
	if !errors.Is(err, ErrInvalidTaskID) {
		t.Errorf("%s error = %v, want invalid task id error", operation, err)
	}
}
