package orbitald

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	RuntimeNamespace   = "orbitald"
	DefaultListenAddr  = ":8080"
	DefaultStateDir    = "/var/lib/orbitald"
	DefaultSnapshotter = "overlayfs"
	DefaultRunTimeout  = 10 * time.Minute
	DefaultPollEvery   = time.Second
	DefaultMaxLogBytes = 128 * 1024
)

var Version = "dev"
var idSeq uint64

const (
	WindowPending = "pending"
	WindowRunning = "running"
	WindowSuccess = "success"
	WindowFailed  = "failed"
	WindowExpired = "expired"
)

type Config struct {
	ListenAddr      string
	StateDir        string
	ContainerdSock  string
	DockerConfigDir string
	Snapshotter     string
	PollEvery       time.Duration
	MaxConcurrent   int
	MaxLogBytes     int
}

type FunctionSpec struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Command     []string          `json:"command,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	User        string            `json:"user,omitempty"`
	MemoryLimit string            `json:"memory_limit,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
}

func (f FunctionSpec) RunTimeout() (time.Duration, error) {
	if f.Timeout == "" {
		return DefaultRunTimeout, nil
	}
	return time.ParseDuration(f.Timeout)
}

type WindowSpec struct {
	ID       string          `json:"id"`
	Function string          `json:"function"`
	Area     string          `json:"area"`
	StartAt  time.Time       `json:"start_at"`
	EndAt    time.Time       `json:"end_at"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type WindowRecord struct {
	WindowSpec
	Status      string     `json:"status"`
	RunID       string     `json:"run_id,omitempty"`
	TriggeredAt *time.Time `json:"triggered_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type ResultRecord struct {
	ID                string     `json:"id"`
	RunID             string     `json:"run_id"`
	Function          string     `json:"function"`
	WindowID          string     `json:"window_id"`
	Area              string     `json:"area"`
	Status            string     `json:"status"`
	ExitCode          uint32     `json:"exit_code"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        time.Time  `json:"finished_at"`
	PayloadPath       string     `json:"payload_path"`
	OutputDir         string     `json:"output_dir"`
	LogPath           string     `json:"log_path"`
	Error             string     `json:"error,omitempty"`
	UploadConfirmedAt *time.Time `json:"upload_confirmed_at,omitempty"`
}

type PendingResult struct {
	ResultRecord
	Log string `json:"log,omitempty"`
}

type SyncRequest struct {
	Functions      []FunctionSpec `json:"functions,omitempty"`
	Windows        []WindowSpec   `json:"windows,omitempty"`
	ReplaceWindows bool           `json:"replace_windows,omitempty"`
	AckResultIDs   []string       `json:"ack_result_ids,omitempty"`
}

type SyncResponse struct {
	Version        string          `json:"version"`
	NodeTime       time.Time       `json:"node_time"`
	Functions      []FunctionSpec  `json:"functions"`
	Windows        []WindowRecord  `json:"windows"`
	PendingResults []PendingResult `json:"pending_results"`
}

type StateSnapshot struct {
	Version   string         `json:"version"`
	NodeTime  time.Time      `json:"node_time"`
	Functions []FunctionSpec `json:"functions"`
	Windows   []WindowRecord `json:"windows"`
	Results   []ResultRecord `json:"results"`
}

type FunctionDetails struct {
	Function FunctionSpec   `json:"function"`
	Windows  []WindowRecord `json:"windows"`
	Results  []ResultRecord `json:"results"`
}

type ContainerInfo struct {
	ID         string            `json:"id"`
	Image      string            `json:"image"`
	Runtime    string            `json:"runtime"`
	TaskStatus string            `json:"task_status"`
	ExitStatus uint32            `json:"exit_status,omitempty"`
	ExitTime   *time.Time        `json:"exit_time,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type TaskInfo struct {
	ID                string     `json:"id"`
	Function          string     `json:"function"`
	Status            string     `json:"status"`
	Image             string     `json:"image,omitempty"`
	ContainerID       string     `json:"container_id,omitempty"`
	WindowID          string     `json:"window_id,omitempty"`
	WindowStatus      string     `json:"window_status,omitempty"`
	RunID             string     `json:"run_id,omitempty"`
	ResultID          string     `json:"result_id,omitempty"`
	ResultStatus      string     `json:"result_status,omitempty"`
	ExitCode          *uint32    `json:"exit_code,omitempty"`
	Area              string     `json:"area,omitempty"`
	StartAt           *time.Time `json:"start_at,omitempty"`
	EndAt             *time.Time `json:"end_at,omitempty"`
	TriggeredAt       *time.Time `json:"triggered_at,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	PayloadPath       string     `json:"payload_path,omitempty"`
	OutputDir         string     `json:"output_dir,omitempty"`
	LogPath           string     `json:"log_path,omitempty"`
	UploadConfirmedAt *time.Time `json:"upload_confirmed_at,omitempty"`
	Error             string     `json:"error,omitempty"`
}

type TaskListResponse struct {
	Tasks        []TaskInfo `json:"tasks"`
	RuntimeError string     `json:"runtime_error,omitempty"`
}

type TaskLogResponse struct {
	ID      string `json:"id"`
	LogPath string `json:"log_path"`
	Log     string `json:"log"`
}

type TaskStartRequest struct {
	Name       string            `json:"name"`
	Image      string            `json:"image,omitempty"`
	Area       string            `json:"area,omitempty"`
	Payload    json.RawMessage   `json:"payload,omitempty"`
	Duration   string            `json:"duration,omitempty"`
	Memory     string            `json:"memory,omitempty"`
	RunTimeout string            `json:"run_timeout,omitempty"`
	User       string            `json:"user,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

type TaskStartResponse struct {
	Version  string       `json:"version"`
	NodeTime time.Time    `json:"node_time"`
	Window   WindowRecord `json:"window"`
}

type TaskStopResponse struct {
	Stopped []string `json:"stopped"`
}

type StatusResponse struct {
	Status   string        `json:"status"`
	Version  string        `json:"version"`
	NodeTime time.Time     `json:"node_time"`
	State    StateCounts   `json:"state"`
	Runtime  RuntimeStatus `json:"runtime"`
}

type StateCounts struct {
	Functions     int          `json:"functions"`
	Windows       WindowCounts `json:"windows"`
	Results       int          `json:"results"`
	PendingUpload int          `json:"pending_upload"`
}

type WindowCounts struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Expired int `json:"expired"`
}

type RuntimeStatus struct {
	Namespace  string `json:"namespace"`
	Socket     string `json:"socket"`
	Containers int    `json:"containers"`
	Error      string `json:"error,omitempty"`
}

func newID(prefix string) string {
	cleanPrefix := strings.ToLower(prefix)
	cleanPrefix = strings.Map(func(r rune) rune {
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
	}, cleanPrefix)
	seq := atomic.AddUint64(&idSeq, 1)
	return fmt.Sprintf("%s-%s-%06d", cleanPrefix, time.Now().UTC().Format("20060102t150405"), seq)
}
