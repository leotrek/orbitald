package orbitald

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/distribution/reference"
	"github.com/docker/cli/cli/config"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/api/resource"
)

type Executor struct {
	sock            string
	stateDir        string
	dockerConfigDir string
	snapshotter     string
}

func NewExecutor(sock, stateDir, dockerConfigDir, snapshotter string) *Executor {
	return &Executor{
		sock:            sock,
		stateDir:        stateDir,
		dockerConfigDir: dockerConfigDir,
		snapshotter:     snapshotter,
	}
}

func (e *Executor) EnsureImage(ctx context.Context, imageRef string) error {
	client, err := containerd.New(e.sock)
	if err != nil {
		return err
	}
	defer client.Close()

	ctx = namespaces.WithNamespace(ctx, RuntimeNamespace)
	imageName, err := normalizeImageRef(imageRef)
	if err != nil {
		return err
	}

	_, err = ensureImagePresent(ctx, client, imageName, e.snapshotter, e.dockerConfigDir)
	return err
}

func (e *Executor) Run(ctx context.Context, fn FunctionSpec, window WindowRecord) (ResultRecord, error) {
	result := ResultRecord{
		ID:       newID("result"),
		RunID:    window.RunID,
		Function: fn.Name,
		WindowID: window.ID,
		Area:     window.Area,
		Status:   WindowFailed,
	}
	result.StartedAt = time.Now().UTC()
	result.FinishedAt = result.StartedAt

	client, err := containerd.New(e.sock)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	defer client.Close()

	runTimeout, err := fn.RunTimeout()
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	nsCtx := namespaces.WithNamespace(context.Background(), RuntimeNamespace)
	imageName, err := normalizeImageRef(fn.Image)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	image, err := getLocalImage(nsCtx, client, imageName, e.snapshotter)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	runDir := filepath.Join(e.stateDir, "runs", window.RunID)
	outputDir := filepath.Join(runDir, "output")
	payloadPath := filepath.Join(runDir, "payload.json")
	logPath := filepath.Join(runDir, "run.log")

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		result.Error = err.Error()
		return result, err
	}

	payload := []byte("{}")
	if len(window.Payload) > 0 {
		if !json.Valid(window.Payload) {
			err = fmt.Errorf("window %s payload must be valid JSON", window.ID)
			result.Error = err.Error()
			return result, err
		}
		payload = append([]byte(nil), window.Payload...)
	}

	if err := os.WriteFile(payloadPath, payload, 0o644); err != nil {
		result.Error = err.Error()
		return result, err
	}

	result.PayloadPath = payloadPath
	result.OutputDir = outputDir
	result.LogPath = logPath

	env := buildEnv(fn.Env, window)
	specOpts := []oci.SpecOpts{
		oci.WithImageConfig(image),
		oci.WithEnv(env),
		oci.WithHostname(window.RunID),
		oci.WithHostNamespace(specs.NetworkNamespace),
		oci.WithHostResolvconf,
		oci.WithHostHostsFile,
		oci.WithMounts([]specs.Mount{
			{
				Source:      payloadPath,
				Destination: "/var/run/orbitald/payload.json",
				Type:        "bind",
				Options:     []string{"rbind", "ro"},
			},
			{
				Source:      outputDir,
				Destination: "/var/run/orbitald/output",
				Type:        "bind",
				Options:     []string{"rbind", "rw"},
			},
		}),
		oci.WithNoNewPrivileges,
	}

	if len(fn.Command) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(fn.Command...))
	}
	if fn.User != "" {
		specOpts = append(specOpts, oci.WithUser(fn.User))
	}
	if mem, err := parseMemoryLimit(fn.MemoryLimit); err != nil {
		result.Error = err.Error()
		return result, err
	} else if mem != nil {
		specOpts = append(specOpts, withMemoryLimit(mem))
	}

	container, err := client.NewContainer(
		nsCtx,
		window.RunID,
		containerd.WithImage(image),
		containerd.WithSnapshotter(e.snapshotter),
		containerd.WithNewSnapshot(window.RunID+"-snapshot", image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	defer func() {
		_ = container.Delete(nsCtx, containerd.WithSnapshotCleanup)
	}()

	task, err := container.NewTask(nsCtx, cio.LogFile(logPath))
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	waitCh, err := task.Wait(nsCtx)
	if err != nil {
		result.Error = err.Error()
		_, _ = task.Delete(nsCtx)
		return result, err
	}

	started := time.Now().UTC()
	result.StartedAt = started
	if err := task.Start(nsCtx); err != nil {
		result.Error = err.Error()
		_, _ = task.Delete(nsCtx)
		return result, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	var exitStatus containerd.ExitStatus
	select {
	case exitStatus = <-waitCh:
	case <-timeoutCtx.Done():
		result.Error = timeoutCtx.Err().Error()
		stoppedStatus, killErr := stopTask(nsCtx, task, waitCh, 5*time.Second)
		exitStatus = stoppedStatus
		if killErr != nil {
			if result.Error == "" {
				result.Error = killErr.Error()
			} else {
				result.Error = result.Error + ": " + killErr.Error()
			}
		}
	}

	exitCode, finishedAt, waitErr := exitStatus.Result()
	result.ExitCode = exitCode
	result.FinishedAt = finishedAt.UTC()
	if waitErr != nil {
		if result.Error == "" {
			result.Error = waitErr.Error()
		} else {
			result.Error = result.Error + ": " + waitErr.Error()
		}
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	if result.Error == "" && exitCode == 0 {
		result.Status = WindowSuccess
	} else {
		result.Status = WindowFailed
		if result.Error == "" {
			result.Error = fmt.Sprintf("exit code %d", exitCode)
		}
	}

	if _, err := task.Delete(nsCtx); err != nil && !errdefs.IsNotFound(err) {
		if result.Error == "" {
			result.Error = err.Error()
		}
	}

	if result.Status == WindowSuccess {
		return result, nil
	}

	return result, errorsFrom(result.Error)
}

func ensureImagePresent(ctx context.Context, client *containerd.Client, imageName, snapshotter, dockerConfigDir string) (containerd.Image, error) {
	resolver, err := dockerResolver(dockerConfigDir)
	if err != nil {
		return nil, err
	}

	image, err := client.GetImage(ctx, imageName)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, err
		}
		image, err = pullImage(ctx, client, resolver, imageName)
		if err != nil {
			return nil, err
		}
	}

	return ensureUnpacked(ctx, image, snapshotter)
}

func getLocalImage(ctx context.Context, client *containerd.Client, imageName, snapshotter string) (containerd.Image, error) {
	image, err := client.GetImage(ctx, imageName)
	if err != nil {
		return nil, fmt.Errorf("image %s is not present locally: %w", imageName, err)
	}
	return ensureUnpacked(ctx, image, snapshotter)
}

func pullImage(ctx context.Context, client *containerd.Client, resolver remotes.Resolver, imageName string) (containerd.Image, error) {
	opts := []containerd.RemoteOpt{containerd.WithPullUnpack}
	if resolver != nil {
		opts = append(opts, containerd.WithResolver(resolver))
	}
	image, err := client.Pull(ctx, imageName, opts...)
	if err != nil {
		return nil, fmt.Errorf("pull %s: %w", imageName, err)
	}
	return image, nil
}

func ensureUnpacked(ctx context.Context, image containerd.Image, snapshotter string) (containerd.Image, error) {
	unpacked, err := image.IsUnpacked(ctx, snapshotter)
	if err != nil {
		return nil, wrapSnapshotterError("check image unpack", snapshotter, err)
	}
	if unpacked {
		return image, nil
	}
	if err := image.Unpack(ctx, snapshotter); err != nil {
		return nil, wrapSnapshotterError("unpack image", snapshotter, err)
	}
	return image, nil
}

func wrapSnapshotterError(action, snapshotter string, err error) error {
	if strings.Contains(err.Error(), "operation not permitted") {
		return fmt.Errorf("%s with snapshotter %q: %w; orbitald needs mount privileges for this containerd snapshotter", action, snapshotter, err)
	}
	return fmt.Errorf("%s with snapshotter %q: %w", action, snapshotter, err)
}

func dockerResolver(dockerConfigDir string) (remotes.Resolver, error) {
	configPath := filepath.Join(dockerConfigDir, config.ConfigFileName)
	if _, err := os.Stat(configPath); err != nil {
		if errorsIsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	cfg, err := config.Load(dockerConfigDir)
	if err != nil {
		return nil, err
	}

	credFunc := func(host string) (string, string, error) {
		if host == "registry-1.docker.io" {
			host = "https://index.docker.io/v1/"
		}
		auth, err := cfg.GetAuthConfig(host)
		if err != nil {
			return "", "", err
		}
		if auth.IdentityToken != "" {
			return "", auth.IdentityToken, nil
		}
		return auth.Username, auth.Password, nil
	}

	authOpts := []docker.AuthorizerOpt{docker.WithAuthCreds(credFunc)}
	return docker.NewResolver(docker.ResolverOptions{
		Hosts: docker.ConfigureDefaultRegistries(docker.WithAuthorizer(docker.NewDockerAuthorizer(authOpts...))),
	}), nil
}

func stopTask(ctx context.Context, task containerd.Task, waitCh <-chan containerd.ExitStatus, gracePeriod time.Duration) (containerd.ExitStatus, error) {
	if err := task.Kill(ctx, unix.SIGTERM, containerd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
		return containerd.ExitStatus{}, err
	}

	timer := time.NewTimer(gracePeriod)
	defer timer.Stop()

	select {
	case status := <-waitCh:
		return status, nil
	case <-timer.C:
		if err := task.Kill(ctx, unix.SIGKILL, containerd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
			return containerd.ExitStatus{}, err
		}
		return <-waitCh, nil
	}
}

func buildEnv(base map[string]string, window WindowRecord) []string {
	env := map[string]string{}
	for k, v := range base {
		env[k] = v
	}
	env["ORBITALD_WINDOW_ID"] = window.ID
	env["ORBITALD_FUNCTION"] = window.Function
	env["ORBITALD_AREA"] = window.Area
	env["ORBITALD_PAYLOAD_PATH"] = "/var/run/orbitald/payload.json"
	env["ORBITALD_OUTPUT_DIR"] = "/var/run/orbitald/output"

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+env[key])
	}
	return values
}

func normalizeImageRef(imageRef string) (string, error) {
	parsed, err := reference.ParseNormalizedNamed(imageRef)
	if err != nil {
		return "", err
	}
	return reference.TagNameOnly(parsed).String(), nil
}

func parseMemoryLimit(limit string) (*specs.LinuxMemory, error) {
	if strings.TrimSpace(limit) == "" {
		return nil, nil
	}

	qty, err := resource.ParseQuantity(limit)
	if err != nil {
		return nil, fmt.Errorf("memory_limit %q: %w", limit, err)
	}
	value := qty.Value()
	return &specs.LinuxMemory{Limit: &value}, nil
}

func withMemoryLimit(mem *specs.LinuxMemory) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, spec *oci.Spec) error {
		if spec.Linux == nil {
			spec.Linux = &specs.Linux{}
		}
		if spec.Linux.Resources == nil {
			spec.Linux.Resources = &specs.LinuxResources{}
		}
		spec.Linux.Resources.Memory = mem
		return nil
	}
}

func errorsFrom(message string) error {
	if message == "" {
		return nil
	}
	return fmt.Errorf("%s", message)
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
