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
	ListenAddr     string
	StateDir       string
	ContainerdSock string
	Snapshotter    string
	PollEvery      time.Duration
	MaxConcurrent  int
	MaxLogBytes    int
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
