package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrInvalidTaskID = errors.New("invalid task id")

type Task struct {
	TaskID          string    `json:"task_id"`
	Repo            string    `json:"repo"`
	RepoPath        string    `json:"repo_path,omitempty"`
	SessionKind     string    `json:"session_kind,omitempty"`
	WorktreeManaged bool      `json:"worktree_managed,omitempty"`
	Branch          string    `json:"branch"`
	Worktree        string    `json:"worktree"`
	TmuxSession     string    `json:"tmux_session"`
	ContainerName   string    `json:"container_name"`
	TokenID         string    `json:"token_id"`
	TokenName       string    `json:"token_name"`
	GitLabProjectID string    `json:"gitlab_project_id"`
	GitLabHost      string    `json:"gitlab_host"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type Store struct {
	stateDir string
}

func NewStore(stateDir string) *Store {
	return &Store{stateDir: stateDir}
}

func (s *Store) Save(task Task) error {
	if err := validateTaskID(task.TaskID); err != nil {
		return err
	}

	if err := s.ensureTasksDir(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	path := s.taskPath(task.TaskID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}

	return os.Chmod(path, 0600)
}

func (s *Store) Load(taskID string) (Task, error) {
	if err := validateTaskID(taskID); err != nil {
		return Task{}, err
	}

	data, err := os.ReadFile(s.taskPath(taskID))
	if err != nil {
		return Task{}, err
	}

	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return Task{}, err
	}

	return task, nil
}

func (s *Store) List() ([]Task, error) {
	entries, err := os.ReadDir(s.tasksDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []Task{}, nil
		}
		return nil, err
	}

	tasks := make([]Task, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		taskID := entry.Name()[:len(entry.Name())-len(".json")]
		task, err := s.Load(taskID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].TaskID < tasks[j].TaskID
	})

	return tasks, nil
}

func (s *Store) Delete(taskID string) error {
	if err := validateTaskID(taskID); err != nil {
		return err
	}

	return os.Remove(s.taskPath(taskID))
}

func (s *Store) ensureTasksDir() error {
	if err := os.MkdirAll(s.tasksDir(), 0700); err != nil {
		return err
	}
	if err := os.Chmod(s.stateDir, 0700); err != nil {
		return err
	}
	return os.Chmod(s.tasksDir(), 0700)
}

func (s *Store) tasksDir() string {
	return filepath.Join(s.stateDir, "tasks")
}

func (s *Store) taskPath(taskID string) string {
	return filepath.Join(s.tasksDir(), taskID+".json")
}

func validateTaskID(taskID string) error {
	if taskID == "" || filepath.IsAbs(taskID) || strings.ContainsAny(taskID, `/\`) {
		return fmt.Errorf("%w: %q", ErrInvalidTaskID, taskID)
	}

	return nil
}
