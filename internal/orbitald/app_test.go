package orbitald

import (
	"path/filepath"
	"testing"
)

func TestNewDefaultsDockerConfigDirUnderStateDir(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")

	app, err := New(Config{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(stateDir, ".docker")
	if app.cfg.DockerConfigDir != want {
		t.Fatalf("expected docker config dir %q, got %q", want, app.cfg.DockerConfigDir)
	}

	executor, ok := app.executor.(*Executor)
	if !ok {
		t.Fatalf("expected executor type *Executor, got %T", app.executor)
	}
	if executor.dockerConfigDir != want {
		t.Fatalf("expected executor docker config dir %q, got %q", want, executor.dockerConfigDir)
	}
}

func TestNewResolvesCustomDockerConfigDir(t *testing.T) {
	customDir := filepath.Join(t.TempDir(), "docker-config")

	app, err := New(Config{
		StateDir:        t.TempDir(),
		DockerConfigDir: customDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	if app.cfg.DockerConfigDir != customDir {
		t.Fatalf("expected docker config dir %q, got %q", customDir, app.cfg.DockerConfigDir)
	}
}
