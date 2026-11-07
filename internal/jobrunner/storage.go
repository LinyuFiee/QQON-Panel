package jobrunner

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"panel/internal/lock"
	"panel/internal/platform"
)

type document struct {
	Format int     `json:"format"`
	Tasks  []*Task `json:"tasks"`
}

func prepareStorage(dataDir string) (string, func(), error) {
	if dataDir == "" {
		return "", nil, errors.New("jobs: data directory is required")
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(absolute, "jobs")
	// Refuse links at every existing ancestor before creating anything. The
	// Windows platform helper additionally rejects junctions and validates ACLs.
	for parent := path; ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", nil, errors.New("jobs: state path must not contain links or non-directories")
		}
		if next := filepath.Dir(parent); next == parent {
			break
		}
	}
	if err := platform.PreparePrivateDirectory(path); err != nil {
		return "", nil, err
	}
	if err := checkOwner(path, true); err != nil {
		return "", nil, err
	}
	release, err := lock.Acquire(path)
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(path, "state.json"), release, nil
}

func loadState(path string) ([]*Task, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return []*Task{}, nil
	} else if err != nil {
		return nil, err
	}
	if err := platform.CheckPrivateFile(path); err != nil {
		return nil, err
	}
	if err := checkOwner(path, false); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || before.Size() > maxStateBytes {
		return nil, errors.New("jobs: state exceeds 3 MiB or cannot be read")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, errors.New("jobs: state changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(body) > maxStateBytes || !utf8.Valid(body) {
		return nil, errors.New("jobs: invalid or oversized state")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var saved document
	if err := decoder.Decode(&saved); err != nil {
		return nil, fmt.Errorf("jobs: invalid state JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("jobs: state contains trailing JSON")
	}
	if saved.Format != 1 || saved.Tasks == nil || len(saved.Tasks) > historyLimit {
		return nil, errors.New("jobs: invalid state format or history bound")
	}
	seen := make(map[string]bool, len(saved.Tasks))
	for _, task := range saved.Tasks {
		if err := validateTask(task); err != nil {
			return nil, err
		}
		if seen[task.ID] {
			return nil, errors.New("jobs: duplicate task ID")
		}
		seen[task.ID] = true
	}
	return saved.Tasks, nil
}

func validateTask(task *Task) error {
	bad := errors.New("jobs: invalid persisted task")
	if task == nil {
		return bad
	}
	id, err := hex.DecodeString(task.ID)
	if err != nil || len(id) != 16 || task.ID != hex.EncodeToString(id) {
		return bad
	}
	if !descriptor(task.Action, 64) || !descriptor(task.Target, 256) || task.Progress < 0 || task.Progress > 100 || task.CreatedAt.IsZero() || task.CreatedAt.After(time.Now().Add(time.Minute)) {
		return bad
	}
	if !terminal(task.Status) && task.Status != "queued" && task.Status != "running" {
		return bad
	}
	if terminal(task.Status) && task.FinishedAt == nil || task.Status == "running" && task.StartedAt == nil {
		return bad
	}
	if len(task.Stage) > 64 || len(task.Message) > 1024 || len(task.Error) > 2048 || len(task.Logs) > 1024 || task.Logs == nil {
		return bad
	}
	for _, line := range task.Logs {
		if len(line) > 4096 {
			return bad
		}
	}
	encoded, err := json.Marshal(task.Logs)
	if err != nil || len(encoded) > logLimit {
		return bad
	}
	return nil
}

func saveState(path string, tasks []*Task) error {
	if len(tasks) > historyLimit {
		return errors.New("jobs: history exceeds limit")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("jobs: state destination is not a regular file")
		}
		if err := platform.CheckPrivateFile(path); err != nil {
			return err
		}
		if err := checkOwner(path, false); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if tasks == nil {
		tasks = []*Task{}
	}
	body, err := json.Marshal(document{1, tasks})
	if err != nil {
		return err
	}
	if len(body) > maxStateBytes {
		return errors.New("jobs: encoded state exceeds 3 MiB")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if err := platform.CheckPrivateFile(file.Name()); err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceState(file.Name(), path)
}
