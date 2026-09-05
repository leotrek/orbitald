package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/orbitald/orbitald/internal/orbitald"
)

const defaultEndpoint = "http://127.0.0.1:8080"

type cli struct {
	endpoint   string
	timeout    time.Duration
	jsonOutput bool
	out        io.Writer
	err        io.Writer
	httpClient *http.Client
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
		endpoint: defaultEndpoint,
		timeout:  5 * time.Second,
		out:      out,
		err:      errOut,
	}

	flags := flag.NewFlagSet("obd", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() { printUsage(errOut) }
	flags.StringVar(&cfg.endpoint, "addr", cfg.endpoint, "orbitald HTTP endpoint")
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
	case "images":
		err = cfg.images(commandArgs)
	case "image":
		err = cfg.image(commandArgs)
	case "task":
		err = cfg.task(commandArgs)
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
  obd [global flags] images
  obd [global flags] image inspect NAME
  obd [global flags] task list [TASK_NAME]
  obd [global flags] task inspect TARGET
  obd [global flags] task describe TARGET
  obd [global flags] task logs TARGET [--tail N]
  obd [global flags] task start NAME [flags]
  obd [global flags] task stop TARGET

Global flags:
  --addr string        orbitald HTTP endpoint (default http://127.0.0.1:8080)
  --timeout duration   command timeout (default 5s)
  --json               print JSON where supported

Examples:
  obd version
  obd status
  obd images
  obd image inspect capture
  obd task list
  obd task list capture
  obd task logs capture-20260905t120000-000001
  obd task start capture --payload '{"camera":"nadir"}'
  obd task start capture --image ghcr.io/acme/capture:latest
  obd task stop capture-20260905t120000-000001
`)
}

func (c *cli) help(args []string) error {
	if len(args) == 0 {
		printUsage(c.out)
		return nil
	}

	switch args[0] {
	case "images", "image":
		fmt.Fprint(c.out, `Usage:
  obd images
  obd image inspect NAME

Images are registered runnable function specs: name, OCI image, env, command, user, memory, and timeout.
`)
	case "task":
		fmt.Fprint(c.out, `Usage:
  obd task list [TASK_NAME]
  obd task inspect TARGET
  obd task describe TARGET
  obd task logs TARGET [--tail N]
  obd task start NAME [flags]
  obd task stop TARGET

Tasks include pending, running, stopped, failed, and expired orbitald executions. Task name is the registered runnable name used to start the task.

Task start flags:
  --image string       image to register before starting
  --area string        window area value (default manual)
  --payload string     JSON payload or @path (default {})
  --duration duration  manual run window duration (default 10m)
  --memory string      memory limit when --image is provided
  --run-timeout string run timeout when --image is provided
  --user string        runtime user when --image is provided
  --arg string         command arg when --image is provided; repeatable
  --env KEY=VALUE      environment variable when --image is provided; repeatable
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

	var status orbitald.StatusResponse
	err := c.getJSON("/v1/status", &status)

	if c.jsonOutput {
		if err != nil {
			return writePrettyJSON(c.out, map[string]any{
				"endpoint":     c.endpoint,
				"daemon_error": err.Error(),
			})
		}
		return writePrettyJSON(c.out, status)
	}

	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "orbitald")
	fmt.Fprintf(c.out, "  daemon: %s\n", status.Status)
	fmt.Fprintf(c.out, "  version: %s\n", status.Version)
	fmt.Fprintf(c.out, "  node time: %s\n", status.NodeTime.Format(time.RFC3339))
	fmt.Fprintf(c.out, "  endpoint: %s\n", c.endpoint)

	printStateCounts(c.out, status.State)

	fmt.Fprintln(c.out, "containerd")
	fmt.Fprintf(c.out, "  socket: %s\n", status.Runtime.Socket)
	fmt.Fprintf(c.out, "  namespace: %s\n", status.Runtime.Namespace)
	if status.Runtime.Error != "" {
		fmt.Fprintf(c.out, "  status: unavailable (%v)\n", status.Runtime.Error)
	} else {
		fmt.Fprintf(c.out, "  containers: %d\n", status.Runtime.Containers)
	}
	return nil
}

func (c *cli) images(args []string) error {
	if len(args) == 0 {
		return c.imageList(nil)
	}
	if len(args) == 1 && args[0] == "help" {
		return c.help([]string{"image"})
	}
	return fmt.Errorf("usage: obd images")
}

func (c *cli) image(args []string) error {
	if len(args) == 0 {
		return c.help([]string{"image"})
	}

	switch args[0] {
	case "inspect":
		return c.imageInspect(args[1:])
	case "help":
		return c.help([]string{"image"})
	default:
		return fmt.Errorf("unknown image command %q", args[0])
	}
}

func (c *cli) task(args []string) error {
	if len(args) == 0 {
		return c.help([]string{"task"})
	}

	switch args[0] {
	case "list":
		return c.taskList(args[1:])
	case "inspect", "describe":
		return c.taskInspect(args[1:])
	case "logs":
		return c.taskLogs(args[1:])
	case "start":
		return c.taskStart(args[1:])
	case "stop":
		return c.taskStop(args[1:])
	case "help":
		return c.help([]string{"task"})
	default:
		return fmt.Errorf("unknown task command %q", args[0])
	}
}

func (c *cli) imageList(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: obd images")
	}
	return c.printImages()
}

func (c *cli) printImages() error {
	var functions []orbitald.FunctionSpec
	if err := c.getJSON("/v1/images", &functions); err != nil {
		return err
	}

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

func (c *cli) imageInspect(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd image inspect NAME")
	}
	return c.printFunctionInfo(args[0])
}

func (c *cli) printFunctionInfo(name string) error {
	var details orbitald.FunctionDetails
	if err := c.getJSON("/v1/images/"+pathEscape(name), &details); err != nil {
		return err
	}
	fn := details.Function

	if c.jsonOutput {
		return writePrettyJSON(c.out, details)
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

	fmt.Fprintf(c.out, "  windows: %d\n", len(details.Windows))
	for _, window := range details.Windows {
		fmt.Fprintf(c.out, "    %-8s %s %s\n", window.Status, window.ID, window.RunID)
	}
	fmt.Fprintf(c.out, "  results: %d\n", len(details.Results))
	for _, result := range details.Results {
		fmt.Fprintf(c.out, "    %-8s exit=%d %s %s\n", result.Status, result.ExitCode, result.ID, result.WindowID)
	}
	return nil
}

func (c *cli) taskList(args []string) error {
	return c.listTaskInfos(args, "usage: obd task list [TASK_NAME]")
}

func (c *cli) listTaskInfos(args []string, usage string) error {
	if len(args) > 1 {
		return fmt.Errorf("%s", usage)
	}
	var taskName string
	if len(args) == 1 {
		taskName = args[0]
	}

	path := "/v1/tasks"
	if taskName != "" {
		path += "?function=" + queryEscape(taskName)
	}
	var response orbitald.TaskListResponse
	if err := c.getJSON(path, &response); err != nil {
		return err
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, response)
	}
	if response.RuntimeError != "" {
		fmt.Fprintf(c.err, "WARN: runtime state omitted: %v\n", response.RuntimeError)
	}

	fmt.Fprintf(c.out, "%-32s %-20s %-9s %-5s %-56s %-20s %s\n", "TASK ID", "TASK NAME", "STATUS", "EXIT", "IMAGE", "STARTED", "ERROR")
	for _, instance := range response.Tasks {
		exitCode := "-"
		if instance.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *instance.ExitCode)
		}
		fmt.Fprintf(
			c.out,
			"%-32s %-20s %-9s %-5s %-56s %-20s %s\n",
			shorten(instance.ID, 32),
			shorten(valueOrDash(instance.Function), 20),
			instance.Status,
			exitCode,
			shorten(valueOrDash(instance.Image), 56),
			formatOptionalTime(instance.StartedAt),
			shorten(valueOrDash(instance.Error), 72),
		)
	}
	return nil
}

func (c *cli) taskInspect(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd task inspect TARGET")
	}
	return c.printTaskInfo(args[0])
}

func (c *cli) printTaskInfo(target string) error {
	var info orbitald.TaskInfo
	if err := c.getJSON("/v1/tasks/"+pathEscape(target), &info); err != nil {
		return err
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, info)
	}

	fmt.Fprintf(c.out, "task id: %s\n", info.ID)
	printOptional(c.out, "  task name", info.Function)
	fmt.Fprintf(c.out, "  status: %s\n", info.Status)
	printOptional(c.out, "  image", info.Image)
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
	printOptional(c.out, "  log", info.LogPath)
	printOptionalTime(c.out, "  uploaded", info.UploadConfirmedAt)
	printOptional(c.out, "  error", info.Error)
	return nil
}

func (c *cli) taskLogs(args []string) error {
	return c.printRunLogs(args, "usage: obd task logs TARGET [--tail N]")
}

func (c *cli) printRunLogs(args []string, usage string) error {
	target, tail, err := parseRunLogsArgs(args, usage)
	if err != nil {
		return err
	}

	path := "/v1/tasks/" + pathEscape(target) + "/logs"
	if tail >= 0 {
		path += "?tail=" + strconv.Itoa(tail)
	}
	var response orbitald.TaskLogResponse
	if err := c.getJSON(path, &response); err != nil {
		return err
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, response)
	}

	_, err = c.out.Write([]byte(response.Log))
	return err
}

func (c *cli) taskStart(args []string) error {
	return c.startTask(args, "obd task start NAME [flags]", "task start")
}

func (c *cli) startTask(args []string, usage, flagSetName string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: %s", usage)
	}
	name := args[0]

	var command stringList
	var envValues stringList
	var image, area, payload, memory, runTimeout, user string
	var duration time.Duration

	flags := flag.NewFlagSet(flagSetName, flag.ContinueOnError)
	flags.SetOutput(c.err)
	flags.StringVar(&image, "image", "", "image to register before starting")
	flags.StringVar(&area, "area", "manual", "window area value")
	flags.StringVar(&payload, "payload", "{}", "JSON payload or @path")
	flags.DurationVar(&duration, "duration", 10*time.Minute, "manual run window duration")
	flags.StringVar(&memory, "memory", "", "memory limit when --image is provided")
	flags.StringVar(&runTimeout, "run-timeout", "", "run timeout when --image is provided")
	flags.StringVar(&user, "user", "", "runtime user when --image is provided")
	flags.Var(&command, "arg", "command arg when --image is provided; repeatable")
	flags.Var(&envValues, "env", "environment variable KEY=VALUE when --image is provided; repeatable")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	payloadBytes, err := readPayload(payload)
	if err != nil {
		return err
	}

	request := orbitald.TaskStartRequest{
		Name:     name,
		Area:     area,
		Payload:  payloadBytes,
		Duration: duration.String(),
	}
	if image != "" {
		request.Image = image
		if len(command) > 0 {
			request.Command = append([]string(nil), command...)
		}
		if len(envValues) > 0 {
			env, err := parseEnv(envValues)
			if err != nil {
				return err
			}
			request.Env = env
		}
		if memory != "" {
			request.Memory = memory
		}
		if runTimeout != "" {
			request.RunTimeout = runTimeout
		}
		if user != "" {
			request.User = user
		}
	}

	var response orbitald.TaskStartResponse
	if err := c.postJSON("/v1/tasks", request, &response); err != nil {
		return err
	}

	if c.jsonOutput {
		return writePrettyJSON(c.out, response)
	}
	fmt.Fprintf(c.out, "queued task %s\n", response.Window.ID)
	fmt.Fprintf(c.out, "task name: %s\n", response.Window.Function)
	fmt.Fprintf(c.out, "window: %s to %s\n", response.Window.StartAt.Format(time.RFC3339), response.Window.EndAt.Format(time.RFC3339))
	return nil
}

func (c *cli) taskStop(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: obd task stop TARGET")
	}
	target := args[0]

	var response orbitald.TaskStopResponse
	if err := c.postJSON("/v1/tasks/"+pathEscape(target)+"/stop", map[string]any{}, &response); err != nil {
		return err
	}
	for _, id := range response.Stopped {
		fmt.Fprintf(c.out, "stopped %s\n", id)
	}
	return nil
}

func parseRunLogsArgs(args []string, usage string) (string, int, error) {
	target := ""
	tail := -1

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--tail":
			i++
			if i >= len(args) {
				return "", 0, fmt.Errorf("%s", usage)
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
		return "", 0, fmt.Errorf("%s", usage)
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

func printStateCounts(w io.Writer, counts orbitald.StateCounts) {
	fmt.Fprintf(w, "  functions: %d\n", counts.Functions)
	fmt.Fprintf(w, "  windows: pending=%d running=%d success=%d failed=%d expired=%d\n",
		counts.Windows.Pending,
		counts.Windows.Running,
		counts.Windows.Success,
		counts.Windows.Failed,
		counts.Windows.Expired,
	)
	fmt.Fprintf(w, "  results: %d pending_upload=%d\n", counts.Results, counts.PendingUpload)
}

func pathEscape(value string) string {
	return url.PathEscape(value)
}

func queryEscape(value string) string {
	return url.QueryEscape(value)
}
