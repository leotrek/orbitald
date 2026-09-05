package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/orbitald/orbitald/internal/orbitald"
)

const defaultEndpoint = "http://127.0.0.1:8080"

type cli struct {
	endpoint       string
	containerdSock string
	stateDir       string
	timeout        time.Duration
	jsonOutput     bool
	out            io.Writer
	err            io.Writer
	httpClient     *http.Client
}

type healthResponse struct {
	Status   string    `json:"status"`
	Version  string    `json:"version"`
	NodeTime time.Time `json:"node_time"`
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errOut io.Writer) int {
	cfg := cli{
		endpoint:       defaultEndpoint,
		containerdSock: "/run/containerd/containerd.sock",
		stateDir:       orbitald.DefaultStateDir,
		timeout:        5 * time.Second,
		out:            out,
		err:            errOut,
	}

	flags := flag.NewFlagSet("obd", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() { printUsage(errOut) }
	flags.StringVar(&cfg.endpoint, "addr", cfg.endpoint, "orbitald HTTP endpoint")
	flags.StringVar(&cfg.containerdSock, "containerd-sock", cfg.containerdSock, "containerd socket path")
	flags.StringVar(&cfg.stateDir, "state-dir", cfg.stateDir, "orbitald state directory for local run logs")
	flags.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "command timeout")
	flags.BoolVar(&cfg.jsonOutput, "json", false, "print JSON where supported")

	if parseErr := flags.Parse(args); parseErr != nil {
		return 2
	}

	cfg.endpoint = normalizeEndpoint(cfg.endpoint)
	cfg.httpClient = &http.Client{Timeout: cfg.timeout}

	rest := flags.Args()
	if len(rest) == 0 {
		printUsage(out)
		return 0
	}

	command := rest[0]
	commandArgs := rest[1:]
	var err error
	switch command {
	case "help", "-h", "--help":
		err = cfg.help(commandArgs)
	case "version":
		err = cfg.version(commandArgs)
	case "status":
		err = cfg.status(commandArgs)
	case "system":
		err = cfg.system(commandArgs)
	case "fn", "function":
		err = cfg.fn(commandArgs)
	case "list", "ls":
		err = cfg.list(commandArgs)
	case "run", "runs":
		err = cfg.runCmd(commandArgs)
	case "log", "logs":
		err = cfg.runLogs(commandArgs)
	case "instance", "instances":
		err = cfg.listRuns(commandArgs)
	case "container", "containers":
		err = cfg.container(commandArgs)
	default:
		fmt.Fprintf(errOut, "unknown command %q\n\n", command)
		printUsage(errOut)
		return 2
	}
	if err != nil {
		fmt.Fprintf(errOut, "ERROR: %v\n", err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  obd [global flags] version
  obd [global flags] status
  obd [global flags] list [FUNCTION]
  obd [global flags] list functions
  obd [global flags] run list [FUNCTION]
  obd [global flags] run info RUN_ID|WINDOW_ID|RESULT_ID
  obd [global flags] run logs RUN_ID|WINDOW_ID|RESULT_ID
  obd [global flags] fn list
  obd [global flags] fn info NAME
  obd [global flags] fn status [NAME]
  obd [global flags] fn start NAME [flags]
  obd [global flags] fn stop NAME|WINDOW_ID|RUN_ID
  obd [global flags] container info ID
  obd [global flags] container status [ID]
  obd [global flags] container start NAME [flags]
  obd [global flags] container stop ID

Global flags:
  --addr string              orbitald HTTP endpoint (default http://127.0.0.1:8080)
  --containerd-sock string   containerd socket path (default /run/containerd/containerd.sock)
  --state-dir string         orbitald state directory for local run logs (default /var/lib/orbitald)
  --timeout duration         command timeout (default 5s)
  --json                     print JSON where supported

Examples:
  obd version
  obd status
  obd fn list
  obd fn status capture
  obd list
  obd list capture
  obd run logs capture-20260905t120000-000001
  obd fn start capture --payload '{"camera":"nadir"}'
  obd fn start capture --image ghcr.io/acme/capture:latest
  obd fn stop capture
`)
}

func (c *cli) help(args []string) error {
	if len(args) == 0 {
		printUsage(c.out)
		return nil
	}

	switch args[0] {
	case "list", "ls":
		fmt.Fprint(c.out, `Usage:
  obd list [FUNCTION]
  obd list runs [FUNCTION]
  obd list functions

The default list command shows runs from the orbitald snapshot. Completed successful runs are shown as stopped, and failed runs are shown as error.
`)
	case "fn", "function":
		fmt.Fprint(c.out, `Usage:
  obd fn list
  obd fn info NAME
  obd fn status [NAME]
  obd fn start NAME [flags]
  obd fn stop NAME|WINDOW_ID|RUN_ID

Function start flags:
  --image string       upsert this function image before starting
  --area string        window area value (default manual)
  --payload string     JSON payload or @path (default {})
  --duration duration  manual run window duration (default 10m)
  --memory string      memory limit when --image is provided
  --run-timeout string function timeout when --image is provided
  --user string        container user when --image is provided
  --arg string         container command arg when --image is provided; repeatable
  --env KEY=VALUE      environment variable when --image is provided; repeatable
`)
	case "run", "runs", "instance", "instances":
		fmt.Fprint(c.out, `Usage:
  obd run list [FUNCTION]
  obd run info RUN_ID|WINDOW_ID|RESULT_ID
  obd run logs RUN_ID|WINDOW_ID|RESULT_ID [--tail N]
  obd run stop RUN_ID|WINDOW_ID|FUNCTION

Runs are read from the orbitald snapshot. Completed successful runs are shown as stopped, and failed runs are shown as error.
`)
	case "container", "containers":
		fmt.Fprint(c.out, `Usage:
  obd container info ID
  obd container status [ID]
  obd container start NAME [flags]
  obd container stop ID

Notes:
  container start is an alias for fn start and starts a function through orbitald.
  container status/info/stop operate on live containerd containers in the orbitald namespace.
`)
	default:
		printUsage(c.out)
	}
	return nil
}

func (c *cli) version(args []string) error {
	if len(args) > 0 && args[0] != "-" {
		return fmt.Errorf("version does not accept arguments")
	}

	health, err := c.getHealth()
	if c.jsonOutput {
		value := map[string]any{
			"cli_version": orbitald.Version,
		}
		if err != nil {
			value["daemon_error"] = err.Error()
		} else {
			value["daemon_version"] = health.Version
			value["daemon_status"] = health.Status
			value["node_time"] = health.NodeTime
		}
		return writePrettyJSON(c.out, value)
	}

	fmt.Fprintf(c.out, "obd %s\n", orbitald.Version)
	if err != nil {
		fmt.Fprintf(c.out, "orbitald unavailable: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.out, "orbitald %s (%s)\n", health.Version, health.Status)
	return nil
}

func (c *cli) status(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("status does not accept arguments")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	health, healthErr := c.getHealth()
	state, stateErr := c.getState()
	systemdState, systemdErr := systemdStatus(ctx)
	containers, containerdErr := c.listContainers(ctx)

	if c.jsonOutput {
		value := map[string]any{
			"endpoint": c.endpoint,
			"containerd": map[string]any{
				"socket": c.containerdSock,
				"error":  errString(containerdErr),
			},
			"systemd": map[string]any{
				"unit":  "orbitald.service",
				"state": systemdState,
				"error": errString(systemdErr),
			},
		}
		if healthErr != nil {
			value["daemon_error"] = healthErr.Error()
		} else {
			value["health"] = health
		}
		if stateErr != nil {
			value["state_error"] = stateErr.Error()
		} else {
			value["state"] = state
		}
		if containerdErr == nil {
			value["containerd"].(map[string]any)["containers"] = containers
		}
		return writePrettyJSON(c.out, value)
	}

	fmt.Fprintln(c.out, "orbitald")
	if healthErr != nil {
		fmt.Fprintf(c.out, "  daemon: unreachable (%v)\n", healthErr)
	} else {
		fmt.Fprintf(c.out, "  daemon: %s\n", health.Status)
		fmt.Fprintf(c.out, "  version: %s\n", health.Version)
		fmt.Fprintf(c.out, "  node time: %s\n", health.NodeTime.Format(time.RFC3339))
	}
	fmt.Fprintf(c.out, "  endpoint: %s\n", c.endpoint)

	if stateErr != nil {
		fmt.Fprintf(c.out, "  state: unavailable (%v)\n", stateErr)
	} else {
		printStateCounts(c.out, state)
	}

	fmt.Fprintln(c.out, "containerd")
	fmt.Fprintf(c.out, "  socket: %s\n", c.containerdSock)
	fmt.Fprintf(c.out, "  namespace: %s\n", orbitald.RuntimeNamespace)
	if containerdErr != nil {
		fmt.Fprintf(c.out, "  status: unavailable (%v)\n", containerdErr)
	} else {
		fmt.Fprintf(c.out, "  containers: %d\n", len(containers))
	}

	fmt.Fprintln(c.out, "systemd")
	if systemdErr != nil {
		fmt.Fprintf(c.out, "  orbitald.service: unavailable (%v)\n", systemdErr)
	} else {
		fmt.Fprintf(c.out, "  orbitald.service: %s\n", systemdState)
	}

	if healthErr != nil || stateErr != nil {
		return errors.New("orbitald daemon is not fully reachable")
	}
	return nil
}

func (c *cli) system(args []string) error {
	if len(args) == 0 {
		return c.status(nil)
	}
	switch args[0] {
	case "status":
		return c.status(args[1:])
	case "version":
		return c.version(args[1:])
	case "help":
		printUsage(c.out)
		return nil
	default:
		return fmt.Errorf("unknown system command %q", args[0])
	}
}

func (c *cli) fn(args []string) error {
	if len(args) == 0 {
		return c.help([]string{"fn"})
	}

	switch args[0] {
	case "list", "ls":
		return c.fnList(args[1:])
	case "info":
		return c.fnInfo(args[1:])
	case "status":
		return c.fnStatus(args[1:])
	case "instances", "runs":
		return c.listRuns(args[1:])
	case "start":
		return c.fnStart(args[1:])
	case "stop":
		return c.fnStop(args[1:])
	case "help":
		return c.help([]string{"fn"})
	default:
		return fmt.Errorf("unknown fn command %q", args[0])
	}
}

func (c *cli) fnList(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: obd fn list")
	}

	state, err := c.getState()
	if err != nil {
		return err
	}

	functions := sortedFunctions(state.Functions)
	if c.jsonOutput {
		return writePrettyJSON(c.out, functions)
	}

	fmt.Fprintf(c.out, "%-24s %-72s %-10s %-10s %-8s %s\n", "NAME", "IMAGE", "TIMEOUT", "MEMORY", "ENV", "COMMAND")
	for _, fn := range functions {
		timeout := fn.Timeout
		if timeout == "" {
			timeout = orbitald.DefaultRunTimeout.String()
		}
		fmt.Fprintf(
			c.out,
			"%-24s %-72s %-10s %-10s %-8d %s\n",
			fn.Name,
			shorten(fn.Image, 72),
			timeout,
			valueOrDash(fn.MemoryLimit),
			len(fn.Env),
			shorten(strings.Join(fn.Command, " "), 48),
		)
	}
	return nil
}

func (c *cli) fnInfo(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd fn info NAME")
	}
	name := args[0]

	state, err := c.getState()
	if err != nil {
		return err
	}
	fn, ok := findFunction(state, name)
	if !ok {
		return fmt.Errorf("function %q not found", name)
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, map[string]any{
			"function": fn,
			"windows":  filterWindows(state.Windows, name),
			"results":  filterResults(state.Results, name),
		})
	}

	fmt.Fprintf(c.out, "function: %s\n", fn.Name)
	fmt.Fprintf(c.out, "  image: %s\n", fn.Image)
	if len(fn.Command) > 0 {
		fmt.Fprintf(c.out, "  command: %s\n", strings.Join(fn.Command, " "))
	}
	if fn.User != "" {
		fmt.Fprintf(c.out, "  user: %s\n", fn.User)
	}
	if fn.MemoryLimit != "" {
		fmt.Fprintf(c.out, "  memory: %s\n", fn.MemoryLimit)
	}
	if fn.Timeout != "" {
		fmt.Fprintf(c.out, "  timeout: %s\n", fn.Timeout)
	}
	fmt.Fprintf(c.out, "  env vars: %d\n", len(fn.Env))

	windows := filterWindows(state.Windows, name)
	results := filterResults(state.Results, name)
	fmt.Fprintf(c.out, "  windows: %d\n", len(windows))
	for _, window := range windows {
		fmt.Fprintf(c.out, "    %-8s %s %s\n", window.Status, window.ID, window.RunID)
	}
	fmt.Fprintf(c.out, "  results: %d\n", len(results))
	for _, result := range results {
		fmt.Fprintf(c.out, "    %-8s exit=%d %s %s\n", result.Status, result.ExitCode, result.ID, result.WindowID)
	}
	return nil
}

func (c *cli) fnStatus(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: obd fn status [NAME]")
	}
	var name string
	if len(args) == 1 {
		name = args[0]
	}

	state, err := c.getState()
	if err != nil {
		return err
	}

	summaries := buildFunctionSummaries(state, name)
	if len(summaries) == 0 && name != "" {
		return fmt.Errorf("function %q not found", name)
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, summaries)
	}

	fmt.Fprintf(c.out, "%-24s %-9s %-9s %-9s %-9s %-9s %-9s %s\n", "NAME", "PENDING", "RUNNING", "SUCCESS", "FAILED", "EXPIRED", "RESULTS", "IMAGE")
	for _, summary := range summaries {
		fmt.Fprintf(
			c.out,
			"%-24s %-9d %-9d %-9d %-9d %-9d %-9d %s\n",
			summary.Name,
			summary.Pending,
			summary.Running,
			summary.Success,
			summary.Failed,
			summary.Expired,
			summary.Results,
			shorten(summary.Image, 64),
		)
	}
	return nil
}

func (c *cli) list(args []string) error {
	if len(args) == 0 {
		return c.listRuns(nil)
	}

	switch args[0] {
	case "fn", "function", "functions":
		return c.fnList(args[1:])
	case "run", "runs", "instance", "instances":
		return c.listRuns(args[1:])
	case "list", "ls", "status":
		return c.listRuns(args[1:])
	case "help":
		return c.help([]string{"list"})
	default:
		return c.listRuns(args)
	}
}

func (c *cli) listRuns(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: obd list [FUNCTION]")
	}
	var function string
	if len(args) == 1 {
		function = args[0]
	}

	state, err := c.getState()
	if err != nil {
		return err
	}

	instances := buildRunInfos(state, function)
	if len(instances) == 0 && function != "" {
		if _, ok := findFunction(state, function); !ok {
			return fmt.Errorf("function %q not found", function)
		}
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, instances)
	}

	fmt.Fprintf(c.out, "%-32s %-20s %-9s %-28s %-28s %-5s %-20s %s\n", "RUN", "FUNCTION", "STATUS", "WINDOW", "RESULT", "EXIT", "STARTED", "ERROR")
	for _, instance := range instances {
		exitCode := "-"
		if instance.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *instance.ExitCode)
		}
		fmt.Fprintf(
			c.out,
			"%-32s %-20s %-9s %-28s %-28s %-5s %-20s %s\n",
			shorten(instance.ID, 32),
			shorten(instance.Function, 20),
			instance.Status,
			shorten(instance.WindowID, 28),
			shorten(valueOrDash(instance.ResultID), 28),
			exitCode,
			formatOptionalTime(instance.StartedAt),
			shorten(valueOrDash(instance.Error), 72),
		)
	}
	return nil
}

func (c *cli) runCmd(args []string) error {
	if len(args) == 0 {
		return c.listRuns(nil)
	}

	switch args[0] {
	case "list", "ls", "status", "instance", "instances":
		return c.listRuns(args[1:])
	case "info", "show":
		return c.runInfo(args[1:])
	case "log", "logs":
		return c.runLogs(args[1:])
	case "stop":
		return c.fnStop(args[1:])
	case "help":
		return c.help([]string{"run"})
	default:
		return fmt.Errorf("unknown run command %q", args[0])
	}
}

func (c *cli) runInfo(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd run info RUN_ID|WINDOW_ID|RESULT_ID")
	}

	state, err := c.getState()
	if err != nil {
		return err
	}

	info, ok := findRunInfo(state, args[0])
	if !ok {
		return fmt.Errorf("run/window/result %q not found", args[0])
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, info)
	}

	fmt.Fprintf(c.out, "run: %s\n", info.ID)
	fmt.Fprintf(c.out, "  function: %s\n", info.Function)
	fmt.Fprintf(c.out, "  status: %s\n", info.Status)
	printOptional(c.out, "  window", info.WindowID)
	printOptional(c.out, "  result", info.ResultID)
	printOptional(c.out, "  run id", info.RunID)
	if info.ExitCode != nil {
		fmt.Fprintf(c.out, "  exit: %d\n", *info.ExitCode)
	}
	printOptional(c.out, "  area", info.Area)
	printOptionalTime(c.out, "  start", info.StartAt)
	printOptionalTime(c.out, "  end", info.EndAt)
	printOptionalTime(c.out, "  triggered", info.TriggeredAt)
	printOptionalTime(c.out, "  started", info.StartedAt)
	printOptionalTime(c.out, "  finished", info.FinishedAt)
	printOptional(c.out, "  payload", info.PayloadPath)
	printOptional(c.out, "  output", info.OutputDir)
	if !runFailedBeforeLog(info) {
		printOptional(c.out, "  log", logPathForRun(c.stateDir, info))
	}
	printOptionalTime(c.out, "  uploaded", info.UploadConfirmedAt)
	printOptional(c.out, "  error", info.Error)
	return nil
}

func (c *cli) runLogs(args []string) error {
	target, tail, err := parseRunLogsArgs(args)
	if err != nil {
		return err
	}

	state, err := c.getState()
	if err != nil {
		return err
	}

	info, ok := findRunInfo(state, target)
	if !ok {
		return fmt.Errorf("run/window/result %q not found", target)
	}
	if runFailedBeforeLog(info) {
		return fmt.Errorf("run failed before log file was created: %s", info.Error)
	}

	logPath := logPathForRun(c.stateDir, info)
	if logPath == "" {
		return fmt.Errorf("run/window/result %q has no log path", target)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("read log %s: %w", logPath, err)
	}
	data = tailLog(data, tail)

	if c.jsonOutput {
		return writePrettyJSON(c.out, map[string]any{
			"id":       info.ID,
			"log_path": logPath,
			"log":      string(data),
		})
	}

	_, err = c.out.Write(data)
	return err
}

func (c *cli) fnStart(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: obd fn start NAME [flags]")
	}
	name := args[0]

	var command stringList
	var envValues stringList
	var image, area, payload, memory, runTimeout, user string
	var duration time.Duration

	flags := flag.NewFlagSet("fn start", flag.ContinueOnError)
	flags.SetOutput(c.err)
	flags.StringVar(&image, "image", "", "upsert this function image before starting")
	flags.StringVar(&area, "area", "manual", "window area value")
	flags.StringVar(&payload, "payload", "{}", "JSON payload or @path")
	flags.DurationVar(&duration, "duration", 10*time.Minute, "manual run window duration")
	flags.StringVar(&memory, "memory", "", "memory limit when --image is provided")
	flags.StringVar(&runTimeout, "run-timeout", "", "function timeout when --image is provided")
	flags.StringVar(&user, "user", "", "container user when --image is provided")
	flags.Var(&command, "arg", "container command arg when --image is provided; repeatable")
	flags.Var(&envValues, "env", "environment variable KEY=VALUE when --image is provided; repeatable")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	state, err := c.getState()
	if err != nil {
		return err
	}

	fn, exists := findFunction(state, name)
	upserts := []orbitald.FunctionSpec(nil)
	if image != "" {
		if !exists {
			fn = orbitald.FunctionSpec{Name: name}
		}
		fn.Image = image
		if len(command) > 0 {
			fn.Command = append([]string(nil), command...)
		}
		if len(envValues) > 0 {
			env, err := parseEnv(envValues)
			if err != nil {
				return err
			}
			fn.Env = env
		}
		if memory != "" {
			fn.MemoryLimit = memory
		}
		if runTimeout != "" {
			fn.Timeout = runTimeout
		}
		if user != "" {
			fn.User = user
		}
		upserts = append(upserts, fn)
	} else if !exists {
		return fmt.Errorf("function %q not found; pass --image to register it before starting", name)
	}

	payloadBytes, err := readPayload(payload)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	window := orbitald.WindowSpec{
		ID:       manualWindowID(name, now),
		Function: name,
		Area:     area,
		StartAt:  now.Add(-1 * time.Second),
		EndAt:    now.Add(duration),
		Payload:  payloadBytes,
	}

	request := orbitald.SyncRequest{
		Functions: upserts,
		Windows:   []orbitald.WindowSpec{window},
	}
	var response orbitald.SyncResponse
	if err := c.postJSON("/v1/contact/sync", request, &response); err != nil {
		return err
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, response)
	}
	fmt.Fprintf(c.out, "queued %s for function %s\n", window.ID, name)
	fmt.Fprintf(c.out, "window: %s to %s\n", window.StartAt.Format(time.RFC3339), window.EndAt.Format(time.RFC3339))
	return nil
}

func (c *cli) fnStop(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd fn stop NAME|WINDOW_ID|RUN_ID")
	}
	target := args[0]

	state, err := c.getState()
	if err != nil {
		return err
	}

	matches := []orbitald.WindowRecord{}
	for _, window := range state.Windows {
		if window.Status != orbitald.WindowRunning {
			continue
		}
		if window.Function == target || window.ID == target || window.RunID == target {
			matches = append(matches, window)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("no running function/window/run matched %q", target)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	for _, window := range matches {
		if window.RunID == "" {
			return fmt.Errorf("running window %q has no run id", window.ID)
		}
		if err := c.stopContainerTask(ctx, window.RunID); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "stopped %s (%s)\n", window.RunID, window.Function)
	}
	return nil
}

func (c *cli) container(args []string) error {
	if len(args) == 0 {
		return c.help([]string{"container"})
	}

	switch args[0] {
	case "info":
		return c.containerInfo(args[1:])
	case "status":
		return c.containerStatus(args[1:])
	case "start":
		return c.fnStart(args[1:])
	case "stop":
		return c.containerStop(args[1:])
	case "help":
		return c.help([]string{"container"})
	default:
		return fmt.Errorf("unknown container command %q", args[0])
	}
}

func (c *cli) containerInfo(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd container info ID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	info, err := c.containerInfoValue(ctx, args[0])
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writePrettyJSON(c.out, info)
	}

	fmt.Fprintf(c.out, "container: %s\n", info.ID)
	fmt.Fprintf(c.out, "  image: %s\n", info.Image)
	fmt.Fprintf(c.out, "  runtime: %s\n", info.Runtime)
	fmt.Fprintf(c.out, "  task: %s\n", info.TaskStatus)
	fmt.Fprintf(c.out, "  created: %s\n", info.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(c.out, "  updated: %s\n", info.UpdatedAt.Format(time.RFC3339))
	return nil
}

func (c *cli) containerStatus(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: obd container status [ID]")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	if len(args) == 1 {
		info, err := c.containerInfoValue(ctx, args[0])
		if err != nil {
			return err
		}
		if c.jsonOutput {
			return writePrettyJSON(c.out, info)
		}
		fmt.Fprintf(c.out, "%s %s %s\n", info.ID, info.TaskStatus, info.Image)
		return nil
	}

	containers, err := c.listContainers(ctx)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writePrettyJSON(c.out, containers)
	}
	fmt.Fprintf(c.out, "%-32s %-12s %s\n", "ID", "TASK", "IMAGE")
	for _, info := range containers {
		fmt.Fprintf(c.out, "%-32s %-12s %s\n", shorten(info.ID, 32), info.TaskStatus, shorten(info.Image, 72))
	}
	return nil
}

func (c *cli) containerStop(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd container stop ID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	if err := c.stopContainerTask(ctx, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "stopped %s\n", args[0])
	return nil
}

type containerInfo struct {
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

func (c *cli) listContainers(ctx context.Context) ([]containerInfo, error) {
	client, nsCtx, err := c.containerdClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	containers, err := client.Containers(nsCtx)
	if err != nil {
		return nil, err
	}

	infos := make([]containerInfo, 0, len(containers))
	for _, item := range containers {
		info, err := containerInfoFrom(nsCtx, item)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].ID < infos[j].ID
	})
	return infos, nil
}

func (c *cli) containerInfoValue(ctx context.Context, id string) (containerInfo, error) {
	client, nsCtx, err := c.containerdClient(ctx)
	if err != nil {
		return containerInfo{}, err
	}
	defer client.Close()

	container, err := client.LoadContainer(nsCtx, id)
	if err != nil {
		return containerInfo{}, err
	}
	return containerInfoFrom(nsCtx, container)
}

func containerInfoFrom(ctx context.Context, item containerd.Container) (containerInfo, error) {
	raw, err := item.Info(ctx)
	if err != nil {
		return containerInfo{}, err
	}

	info := containerInfo{
		ID:         raw.ID,
		Image:      raw.Image,
		Runtime:    raw.Runtime.Name,
		TaskStatus: "stopped",
		CreatedAt:  raw.CreatedAt,
		UpdatedAt:  raw.UpdatedAt,
		Labels:     raw.Labels,
	}

	task, err := item.Task(ctx, nil)
	if errdefs.IsNotFound(err) {
		return info, nil
	}
	if err != nil {
		info.TaskStatus = "unknown"
		return info, nil
	}

	status, err := task.Status(ctx)
	if err != nil {
		info.TaskStatus = "unknown"
		return info, nil
	}
	info.TaskStatus = string(status.Status)
	info.ExitStatus = status.ExitStatus
	if !status.ExitTime.IsZero() {
		exitTime := status.ExitTime
		info.ExitTime = &exitTime
	}
	return info, nil
}

func (c *cli) stopContainerTask(ctx context.Context, id string) error {
	client, nsCtx, err := c.containerdClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	container, err := client.LoadContainer(nsCtx, id)
	if err != nil {
		return err
	}
	task, err := container.Task(nsCtx, nil)
	if errdefs.IsNotFound(err) {
		return fmt.Errorf("container %q has no running task", id)
	}
	if err != nil {
		return err
	}

	waitCh, waitErr := task.Wait(nsCtx)
	if err := task.Kill(nsCtx, syscall.SIGTERM, containerd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if waitErr != nil {
		return nil
	}

	select {
	case <-waitCh:
		return nil
	case <-time.After(3 * time.Second):
		if err := task.Kill(nsCtx, syscall.SIGKILL, containerd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *cli) containerdClient(ctx context.Context) (*containerd.Client, context.Context, error) {
	client, err := containerd.New(c.containerdSock)
	if err != nil {
		return nil, nil, err
	}
	return client, namespaces.WithNamespace(ctx, orbitald.RuntimeNamespace), nil
}

type functionSummary struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Pending int    `json:"pending"`
	Running int    `json:"running"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
	Expired int    `json:"expired"`
	Results int    `json:"results"`
}

type runInfo struct {
	ID                string     `json:"id"`
	Function          string     `json:"function"`
	Status            string     `json:"status"`
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

func buildRunInfos(state orbitald.StateSnapshot, onlyFunction string) []runInfo {
	resultsByWindow := map[string]orbitald.ResultRecord{}
	resultsByRun := map[string]orbitald.ResultRecord{}
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

	instances := make([]runInfo, 0, len(state.Windows)+len(state.Results))
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
		instances = append(instances, runFromWindow(window, result, hasResult))
	}

	for _, result := range state.Results {
		if onlyFunction != "" && result.Function != onlyFunction {
			continue
		}
		if usedResults[result.ID] {
			continue
		}
		instances = append(instances, runFromResult(result))
	}

	sort.Slice(instances, func(i, j int) bool {
		left := effectiveRunTime(instances[i])
		right := effectiveRunTime(instances[j])
		if left.Equal(right) {
			return instances[i].ID < instances[j].ID
		}
		return left.After(right)
	})
	return instances
}

func runFromWindow(window orbitald.WindowRecord, result orbitald.ResultRecord, hasResult bool) runInfo {
	info := runInfo{
		ID:           firstNonEmpty(window.RunID, window.ID),
		Function:     window.Function,
		Status:       normalizeRunStatus(window.Status, ""),
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
		mergeResultIntoRun(&info, result)
	}
	return info
}

func runFromResult(result orbitald.ResultRecord) runInfo {
	info := runInfo{
		ID:       firstNonEmpty(result.RunID, result.ID),
		Function: result.Function,
		Status:   normalizeRunStatus("", result.Status),
		WindowID: result.WindowID,
		RunID:    result.RunID,
	}
	mergeResultIntoRun(&info, result)
	return info
}

func mergeResultIntoRun(info *runInfo, result orbitald.ResultRecord) {
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
	info.Status = normalizeRunStatus(info.WindowStatus, result.Status)
}

func findRunInfo(state orbitald.StateSnapshot, target string) (runInfo, bool) {
	instances := buildRunInfos(state, "")
	for _, instance := range instances {
		if target == instance.ID || target == instance.RunID {
			return instance, true
		}
	}
	for _, instance := range instances {
		if target == instance.WindowID || target == instance.ResultID {
			return instance, true
		}
	}
	return runInfo{}, false
}

func logPathForRun(stateDir string, info runInfo) string {
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

func runFailedBeforeLog(info runInfo) bool {
	return info.LogPath == "" && info.ResultID != "" && info.Error != ""
}

func parseRunLogsArgs(args []string) (string, int, error) {
	target := ""
	tail := -1

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--tail":
			i++
			if i >= len(args) {
				return "", 0, fmt.Errorf("usage: obd run logs RUN_ID|WINDOW_ID|RESULT_ID [--tail N]")
			}
			value, err := parseTailValue(args[i])
			if err != nil {
				return "", 0, err
			}
			tail = value
		case strings.HasPrefix(arg, "--tail="):
			value, err := parseTailValue(strings.TrimPrefix(arg, "--tail="))
			if err != nil {
				return "", 0, err
			}
			tail = value
		case strings.HasPrefix(arg, "-"):
			return "", 0, fmt.Errorf("unknown flag %q", arg)
		default:
			if target != "" {
				return "", 0, fmt.Errorf("unexpected argument %q", arg)
			}
			target = arg
		}
	}

	if target == "" {
		return "", 0, fmt.Errorf("usage: obd run logs RUN_ID|WINDOW_ID|RESULT_ID [--tail N]")
	}
	return target, tail, nil
}

func parseTailValue(value string) (int, error) {
	tail, err := strconv.Atoi(value)
	if err != nil || tail < 0 {
		return 0, fmt.Errorf("tail must be a non-negative integer")
	}
	return tail, nil
}

func tailLog(data []byte, lines int) []byte {
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

func normalizeRunStatus(windowStatus, resultStatus string) string {
	if resultStatus == orbitald.WindowFailed || windowStatus == orbitald.WindowFailed {
		return "error"
	}
	if windowStatus == orbitald.WindowExpired {
		return "expired"
	}
	if windowStatus == orbitald.WindowRunning {
		return "running"
	}
	if windowStatus == orbitald.WindowPending {
		return "pending"
	}
	if resultStatus == orbitald.WindowSuccess || windowStatus == orbitald.WindowSuccess {
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

func buildFunctionSummaries(state orbitald.StateSnapshot, only string) []functionSummary {
	summaries := map[string]*functionSummary{}
	for _, fn := range state.Functions {
		if only != "" && fn.Name != only {
			continue
		}
		copyFn := fn
		summaries[fn.Name] = &functionSummary{Name: copyFn.Name, Image: copyFn.Image}
	}
	for _, window := range state.Windows {
		if only != "" && window.Function != only {
			continue
		}
		summary, ok := summaries[window.Function]
		if !ok {
			summary = &functionSummary{Name: window.Function}
			summaries[window.Function] = summary
		}
		switch window.Status {
		case orbitald.WindowPending:
			summary.Pending++
		case orbitald.WindowRunning:
			summary.Running++
		case orbitald.WindowSuccess:
			summary.Success++
		case orbitald.WindowFailed:
			summary.Failed++
		case orbitald.WindowExpired:
			summary.Expired++
		}
	}
	for _, result := range state.Results {
		if only != "" && result.Function != only {
			continue
		}
		summary, ok := summaries[result.Function]
		if !ok {
			summary = &functionSummary{Name: result.Function}
			summaries[result.Function] = summary
		}
		summary.Results++
	}

	values := make([]functionSummary, 0, len(summaries))
	for _, summary := range summaries {
		values = append(values, *summary)
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i].Name < values[j].Name
	})
	return values
}

func findFunction(state orbitald.StateSnapshot, name string) (orbitald.FunctionSpec, bool) {
	for _, fn := range state.Functions {
		if fn.Name == name {
			return fn, true
		}
	}
	return orbitald.FunctionSpec{}, false
}

func sortedFunctions(functions []orbitald.FunctionSpec) []orbitald.FunctionSpec {
	sorted := append([]orbitald.FunctionSpec(nil), functions...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func filterWindows(windows []orbitald.WindowRecord, name string) []orbitald.WindowRecord {
	filtered := make([]orbitald.WindowRecord, 0, len(windows))
	for _, window := range windows {
		if window.Function == name {
			filtered = append(filtered, window)
		}
	}
	return filtered
}

func filterResults(results []orbitald.ResultRecord, name string) []orbitald.ResultRecord {
	filtered := make([]orbitald.ResultRecord, 0, len(results))
	for _, result := range results {
		if result.Function == name {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func printStateCounts(w io.Writer, state orbitald.StateSnapshot) {
	counts := map[string]int{}
	for _, window := range state.Windows {
		counts[window.Status]++
	}
	pendingUpload := 0
	for _, result := range state.Results {
		if result.UploadConfirmedAt == nil {
			pendingUpload++
		}
	}
	fmt.Fprintf(w, "  functions: %d\n", len(state.Functions))
	fmt.Fprintf(w, "  windows: pending=%d running=%d success=%d failed=%d expired=%d\n",
		counts[orbitald.WindowPending],
		counts[orbitald.WindowRunning],
		counts[orbitald.WindowSuccess],
		counts[orbitald.WindowFailed],
		counts[orbitald.WindowExpired],
	)
	fmt.Fprintf(w, "  results: %d pending_upload=%d\n", len(state.Results), pendingUpload)
}

func effectiveRunTime(info runInfo) time.Time {
	for _, candidate := range []*time.Time{info.StartedAt, info.TriggeredAt, info.StartAt, info.FinishedAt, info.EndAt} {
		if candidate != nil && !candidate.IsZero() {
			return *candidate
		}
	}
	return time.Time{}
}

func (c *cli) getHealth() (healthResponse, error) {
	var health healthResponse
	err := c.getJSON("/healthz", &health)
	return health, err
}

func (c *cli) getState() (orbitald.StateSnapshot, error) {
	var state orbitald.StateSnapshot
	err := c.getJSON("/v1/state", &state)
	return state, err
}

func (c *cli) getJSON(path string, target any) error {
	request, err := http.NewRequest(http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (c *cli) postJSON(path string, body any, target any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func writePrettyJSON(w io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func readPayload(value string) (json.RawMessage, error) {
	if value == "" {
		value = "{}"
	}
	if strings.HasPrefix(value, "@") {
		data, err := os.ReadFile(strings.TrimPrefix(value, "@"))
		if err != nil {
			return nil, err
		}
		value = string(data)
	}
	data := []byte(value)
	if !json.Valid(data) {
		return nil, fmt.Errorf("payload must be valid JSON")
	}
	return json.RawMessage(data), nil
}

func parseEnv(values []string) (map[string]string, error) {
	env := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("env value %q must be KEY=VALUE", value)
		}
		env[key] = val
	}
	return env, nil
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
	return fmt.Sprintf("manual-%s-%s", clean, now.Format("20060102t150405z"))
}

func normalizeEndpoint(value string) string {
	value = strings.TrimRight(value, "/")
	if value == "" {
		return defaultEndpoint
	}
	if strings.HasPrefix(value, ":") {
		return "http://127.0.0.1" + value
	}
	if !strings.Contains(value, "://") {
		return "http://" + value
	}
	return value
}

func shorten(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func printOptional(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "%s: %s\n", label, value)
}

func printOptionalTime(w io.Writer, label string, value *time.Time) {
	if value == nil || value.IsZero() {
		return
	}
	fmt.Fprintf(w, "%s: %s\n", label, value.UTC().Format(time.RFC3339))
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

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func systemdStatus(ctx context.Context) (string, error) {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return "", err
	}
	output, err := exec.CommandContext(ctx, path, "is-active", "orbitald.service").CombinedOutput()
	state := strings.TrimSpace(string(output))
	if state == "" {
		state = "unknown"
	}
	if err != nil {
		return state, err
	}
	return state, nil
}
