package orbitald

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const stateFileName = "state.json"

type persistedState struct {
	Functions map[string]FunctionSpec `json:"functions"`
	Windows   map[string]WindowRecord `json:"windows"`
	Results   []ResultRecord          `json:"results"`
}

type Store struct {
	mu    sync.Mutex
	dir   string
	state persistedState
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	store := &Store{
		dir: dir,
		state: persistedState{
			Functions: map[string]FunctionSpec{},
			Windows:   map[string]WindowRecord{},
			Results:   []ResultRecord{},
		},
	}

	path := filepath.Join(dir, stateFileName)
	if data, err := os.ReadFile(path); err == nil {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &store.state); err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if store.state.Functions == nil {
		store.state.Functions = map[string]FunctionSpec{}
	}
	if store.state.Windows == nil {
		store.state.Windows = map[string]WindowRecord{}
	}
	if store.state.Results == nil {
		store.state.Results = []ResultRecord{}
	}

	if store.recoverLocked(time.Now().UTC()) {
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	}

	return store, nil
}

func (s *Store) Snapshot() StateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return StateSnapshot{
		Version:   Version,
		NodeTime:  time.Now().UTC(),
		Functions: s.functionsLocked(),
		Windows:   s.windowsLocked(),
		Results:   append([]ResultRecord(nil), s.state.Results...),
	}
}

func (s *Store) Function(name string) (FunctionSpec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn, ok := s.state.Functions[name]
	return fn, ok
}

func (s *Store) UpsertFunctions(functions []FunctionSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, fn := range functions {
		if err := validateFunction(fn); err != nil {
			return err
		}
		s.state.Functions[fn.Name] = fn
	}

	return s.saveLocked()
}

func (s *Store) UpsertWindows(windows []WindowSpec, replace bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if replace {
		s.state.Windows = map[string]WindowRecord{}
	}

	for _, window := range windows {
		if err := validateWindow(window); err != nil {
			return err
		}
		if _, ok := s.state.Functions[window.Function]; !ok {
			return fmt.Errorf("window %s references unknown function %s", window.ID, window.Function)
		}
		s.state.Windows[window.ID] = WindowRecord{
			WindowSpec: window,
			Status:     WindowPending,
		}
	}

	return s.saveLocked()
}

func (s *Store) AckResults(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) == 0 {
		return nil
	}

	now := time.Now().UTC()
	for i := range s.state.Results {
		for _, id := range ids {
			if s.state.Results[i].ID == id {
				s.state.Results[i].UploadConfirmedAt = &now
			}
		}
	}

	return s.saveLocked()
}

func (s *Store) ExpireWindows(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for id, window := range s.state.Windows {
		if window.Status == WindowPending && now.After(window.EndAt.UTC()) {
			window.Status = WindowExpired
			window.Error = "window closed before execution"
			finished := now.UTC()
			window.FinishedAt = &finished
			s.state.Windows[id] = window
			changed = true
		}
	}

	if !changed {
		return nil
	}

	return s.saveLocked()
}

func (s *Store) ClaimDueWindow(now time.Time) (WindowRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var selected *WindowRecord
	for _, window := range s.state.Windows {
		if window.Status != WindowPending {
			continue
		}
		start := window.StartAt.UTC()
		end := window.EndAt.UTC()
		if now.Before(start) || now.After(end) {
			continue
		}
		if selected == nil || window.StartAt.Before(selected.StartAt) {
			copyWindow := window
			selected = &copyWindow
		}
	}

	if selected == nil {
		return WindowRecord{}, false, nil
	}

	runID := newID(selected.Function)
	triggered := now.UTC()
	selected.RunID = runID
	selected.Status = WindowRunning
	selected.TriggeredAt = &triggered
	selected.Error = ""
	s.state.Windows[selected.ID] = *selected

	if err := s.saveLocked(); err != nil {
		return WindowRecord{}, false, err
	}

	return *selected, true, nil
}

func (s *Store) CompleteWindow(windowID string, result ResultRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	window, ok := s.state.Windows[windowID]
	if !ok {
		return fmt.Errorf("window %s not found", windowID)
	}

	finished := result.FinishedAt.UTC()
	window.FinishedAt = &finished
	window.RunID = result.RunID
	window.Error = result.Error
	if result.Status == WindowSuccess {
		window.Status = WindowSuccess
	} else {
		window.Status = WindowFailed
	}
	s.state.Windows[windowID] = window
	s.state.Results = append(s.state.Results, result)

	return s.saveLocked()
}

func (s *Store) PendingResults(maxLogBytes int) ([]PendingResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]PendingResult, 0, len(s.state.Results))
	for _, result := range s.state.Results {
		if result.UploadConfirmedAt != nil {
			continue
		}
		pending := PendingResult{ResultRecord: result}
		if logData, err := readBoundedFile(result.LogPath, maxLogBytes); err == nil {
			pending.Log = string(logData)
		}
		results = append(results, pending)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].StartedAt.Before(results[j].StartedAt)
	})

	return results, nil
}

func (s *Store) recoverLocked(now time.Time) bool {
	changed := false

	for id, window := range s.state.Windows {
		switch {
		case window.Status == WindowRunning && now.After(window.EndAt.UTC()):
			window.Status = WindowFailed
			window.Error = "orbitald restarted during execution"
			finished := now.UTC()
			window.FinishedAt = &finished
			s.state.Windows[id] = window
			changed = true
		case window.Status == WindowRunning:
			window.Status = WindowPending
			window.RunID = ""
			window.Error = "orbitald restarted before execution completed"
			window.TriggeredAt = nil
			s.state.Windows[id] = window
			changed = true
		case window.Status == WindowPending && now.After(window.EndAt.UTC()):
			window.Status = WindowExpired
			window.Error = "window closed while orbitald was offline"
			finished := now.UTC()
			window.FinishedAt = &finished
			s.state.Windows[id] = window
			changed = true
		}
	}

	return changed
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := filepath.Join(s.dir, stateFileName+".tmp")
	finalPath := filepath.Join(s.dir, stateFileName)
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpPath, finalPath)
}

func (s *Store) functionsLocked() []FunctionSpec {
	functions := make([]FunctionSpec, 0, len(s.state.Functions))
	for _, fn := range s.state.Functions {
		functions = append(functions, fn)
	}
	sort.Slice(functions, func(i, j int) bool {
		return functions[i].Name < functions[j].Name
	})
	return functions
}

func (s *Store) windowsLocked() []WindowRecord {
	windows := make([]WindowRecord, 0, len(s.state.Windows))
	for _, window := range s.state.Windows {
		windows = append(windows, window)
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].StartAt.Equal(windows[j].StartAt) {
			return windows[i].ID < windows[j].ID
		}
		return windows[i].StartAt.Before(windows[j].StartAt)
	})
	return windows
}

func validateFunction(fn FunctionSpec) error {
	if fn.Name == "" {
		return errors.New("function name is required")
	}
	if fn.Image == "" {
		return fmt.Errorf("function %s image is required", fn.Name)
	}
	if _, err := fn.RunTimeout(); err != nil {
		return fmt.Errorf("function %s timeout: %w", fn.Name, err)
	}
	return nil
}

func validateWindow(window WindowSpec) error {
	if window.ID == "" {
		return errors.New("window id is required")
	}
	if window.Function == "" {
		return fmt.Errorf("window %s function is required", window.ID)
	}
	if window.Area == "" {
		return fmt.Errorf("window %s area is required", window.ID)
	}
	if window.StartAt.IsZero() || window.EndAt.IsZero() {
		return fmt.Errorf("window %s start_at and end_at are required", window.ID)
	}
	if !window.EndAt.After(window.StartAt) {
		return fmt.Errorf("window %s end_at must be after start_at", window.ID)
	}
	return nil
}

func readBoundedFile(path string, max int) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if max > 0 && len(data) > max {
		return data[:max], nil
	}
	return data, nil
}
