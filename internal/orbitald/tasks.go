package orbitald

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func FunctionDetailsFromState(state StateSnapshot, name string) (FunctionDetails, bool) {
	fn, ok := FindFunction(state, name)
	if !ok {
		return FunctionDetails{}, false
	}
	return FunctionDetails{
		Function: fn,
		Windows:  FilterWindows(state.Windows, name),
		Results:  FilterResults(state.Results, name),
	}, true
}

func FindFunction(state StateSnapshot, name string) (FunctionSpec, bool) {
	for _, fn := range state.Functions {
		if fn.Name == name {
			return fn, true
		}
	}
	return FunctionSpec{}, false
}

func FilterWindows(windows []WindowRecord, name string) []WindowRecord {
	filtered := make([]WindowRecord, 0, len(windows))
	for _, window := range windows {
		if window.Function == name {
			filtered = append(filtered, window)
		}
	}
	return filtered
}

func FilterResults(results []ResultRecord, name string) []ResultRecord {
	filtered := make([]ResultRecord, 0, len(results))
	for _, result := range results {
		if result.Function == name {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func BuildTaskInfos(state StateSnapshot, onlyFunction string) []TaskInfo {
	imagesByFunction := map[string]string{}
	for _, fn := range state.Functions {
		imagesByFunction[fn.Name] = fn.Image
	}

	resultsByWindow := map[string]ResultRecord{}
	resultsByRun := map[string]ResultRecord{}
	usedResults := map[string]bool{}
	for _, result := range state.Results {
		if onlyFunction != "" && result.Function != onlyFunction {
			continue
		}
		if result.WindowID != "" {
			if existing, ok := resultsByWindow[result.WindowID]; !ok || result.FinishedAt.After(existing.FinishedAt) {
				resultsByWindow[result.WindowID] = result
			}
		}
		if result.RunID != "" {
			if existing, ok := resultsByRun[result.RunID]; !ok || result.FinishedAt.After(existing.FinishedAt) {
				resultsByRun[result.RunID] = result
			}
		}
	}

	tasks := make([]TaskInfo, 0, len(state.Windows)+len(state.Results))
	for _, window := range state.Windows {
		if onlyFunction != "" && window.Function != onlyFunction {
			continue
		}
		result, hasResult := resultsByWindow[window.ID]
		if !hasResult && window.RunID != "" {
			result, hasResult = resultsByRun[window.RunID]
		}
		if hasResult {
			usedResults[result.ID] = true
		}
		tasks = append(tasks, taskFromWindow(window, result, hasResult))
	}

	for _, result := range state.Results {
		if onlyFunction != "" && result.Function != onlyFunction {
			continue
		}
		if usedResults[result.ID] {
			continue
		}
		tasks = append(tasks, taskFromResult(result))
	}

	for i := range tasks {
		tasks[i].Image = imagesByFunction[tasks[i].Function]
	}
	sortTaskInfos(tasks)
	return tasks
}

func MergeContainerInfos(tasks []TaskInfo, containers []ContainerInfo, onlyFunction string) []TaskInfo {
	byRunID := map[string]int{}
	byTaskID := map[string]int{}
	for i := range tasks {
		if tasks[i].RunID != "" {
			byRunID[tasks[i].RunID] = i
		}
		byTaskID[tasks[i].ID] = i
	}

	for _, container := range containers {
		if index, ok := byRunID[container.ID]; ok {
			tasks[index].ContainerID = container.ID
			if tasks[index].Image == "" {
				tasks[index].Image = container.Image
			}
			continue
		}
		if index, ok := byTaskID[container.ID]; ok {
			tasks[index].ContainerID = container.ID
			if tasks[index].Image == "" {
				tasks[index].Image = container.Image
			}
			continue
		}
		if onlyFunction != "" {
			continue
		}
		tasks = append(tasks, TaskInfo{
			ID:          container.ID,
			Status:      NormalizeContainerTaskStatus(container.TaskStatus),
			Image:       container.Image,
			ContainerID: container.ID,
			RunID:       container.ID,
			StartedAt:   timePtr(container.CreatedAt),
		})
	}

	sortTaskInfos(tasks)
	return tasks
}

func FindTaskInfo(state StateSnapshot, target string) (TaskInfo, bool) {
	tasks := BuildTaskInfos(state, "")
	for _, task := range tasks {
		if target == task.ID || target == task.RunID {
			return task, true
		}
	}
	for _, task := range tasks {
		if target == task.WindowID || target == task.ResultID {
			return task, true
		}
	}
	return TaskInfo{}, false
}

func LogPathForTask(stateDir string, info TaskInfo) string {
	if info.LogPath != "" {
		if filepath.IsAbs(info.LogPath) {
			return info.LogPath
		}
		return filepath.Join(stateDir, info.LogPath)
	}
	if info.RunID != "" {
		return filepath.Join(stateDir, "runs", info.RunID, "run.log")
	}
	return ""
}

func TaskFailedBeforeLog(info TaskInfo) bool {
	return info.LogPath == "" && info.ResultID != "" && info.Error != ""
}

func TailLog(data []byte, lines int) []byte {
	if lines < 0 {
		return data
	}
	if lines == 0 || len(data) == 0 {
		return nil
	}

	trailingNewline := data[len(data)-1] == '\n'
	trimmed := data
	if trailingNewline {
		trimmed = data[:len(data)-1]
	}
	parts := bytes.Split(trimmed, []byte{'\n'})
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	out := bytes.Join(parts, []byte{'\n'})
	if trailingNewline {
		out = append(out, '\n')
	}
	return out
}

func StateCountsFromSnapshot(state StateSnapshot) StateCounts {
	counts := StateCounts{
		Functions: len(state.Functions),
		Results:   len(state.Results),
	}
	for _, window := range state.Windows {
		switch window.Status {
		case WindowPending:
			counts.Windows.Pending++
		case WindowRunning:
			counts.Windows.Running++
		case WindowSuccess:
			counts.Windows.Success++
		case WindowFailed:
			counts.Windows.Failed++
		case WindowExpired:
			counts.Windows.Expired++
		}
	}
	for _, result := range state.Results {
		if result.UploadConfirmedAt == nil {
			counts.PendingUpload++
		}
	}
	return counts
}

func NormalizeTaskStatus(windowStatus, resultStatus string) string {
	if resultStatus == WindowFailed || windowStatus == WindowFailed {
		return "error"
	}
	if windowStatus == WindowExpired {
		return "expired"
	}
	if windowStatus == WindowRunning {
		return "running"
	}
	if windowStatus == WindowPending {
		return "pending"
	}
	if resultStatus == WindowSuccess || windowStatus == WindowSuccess {
		return "stopped"
	}
	if resultStatus != "" {
		return resultStatus
	}
	if windowStatus != "" {
		return windowStatus
	}
	return "unknown"
}

func NormalizeContainerTaskStatus(status string) string {
	switch status {
	case "", "unknown":
		return "unknown"
	case "created", "creating", "running", "paused", "pausing":
		return status
	case "stopped":
		return "stopped"
	default:
		return status
	}
}

func manualWindowID(name string, now time.Time) string {
	clean := strings.ToLower(name)
	clean = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-':
			return r
		default:
			return '-'
		}
	}, clean)
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "function"
	}
	return "manual-" + clean + "-" + now.Format("20060102t150405z")
}

func taskFromWindow(window WindowRecord, result ResultRecord, hasResult bool) TaskInfo {
	info := TaskInfo{
		ID:           firstNonEmpty(window.RunID, window.ID),
		Function:     window.Function,
		Status:       NormalizeTaskStatus(window.Status, ""),
		WindowID:     window.ID,
		WindowStatus: window.Status,
		RunID:        window.RunID,
		Area:         window.Area,
		StartAt:      timePtr(window.StartAt),
		EndAt:        timePtr(window.EndAt),
		TriggeredAt:  window.TriggeredAt,
		FinishedAt:   window.FinishedAt,
		Error:        window.Error,
	}
	if hasResult {
		mergeResultIntoTask(&info, result)
	}
	return info
}

func taskFromResult(result ResultRecord) TaskInfo {
	info := TaskInfo{
		ID:       firstNonEmpty(result.RunID, result.ID),
		Function: result.Function,
		Status:   NormalizeTaskStatus("", result.Status),
		WindowID: result.WindowID,
		RunID:    result.RunID,
	}
	mergeResultIntoTask(&info, result)
	return info
}

func mergeResultIntoTask(info *TaskInfo, result ResultRecord) {
	exitCode := result.ExitCode
	info.ResultID = result.ID
	info.ResultStatus = result.Status
	info.ExitCode = &exitCode
	info.StartedAt = timePtr(result.StartedAt)
	info.FinishedAt = timePtr(result.FinishedAt)
	info.PayloadPath = result.PayloadPath
	info.OutputDir = result.OutputDir
	info.LogPath = result.LogPath
	info.UploadConfirmedAt = result.UploadConfirmedAt
	if result.Error != "" {
		info.Error = result.Error
	}
	info.Status = NormalizeTaskStatus(info.WindowStatus, result.Status)
}

func sortTaskInfos(tasks []TaskInfo) {
	sort.Slice(tasks, func(i, j int) bool {
		left := effectiveTaskTime(tasks[i])
		right := effectiveTaskTime(tasks[j])
		if left.Equal(right) {
			return tasks[i].ID < tasks[j].ID
		}
		return left.After(right)
	})
}

func effectiveTaskTime(info TaskInfo) time.Time {
	for _, candidate := range []*time.Time{info.StartedAt, info.TriggeredAt, info.StartAt, info.FinishedAt, info.EndAt} {
		if candidate != nil && !candidate.IsZero() {
			return *candidate
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "-"
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}
