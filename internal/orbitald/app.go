package orbitald

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"
)

type runtimeExecutor interface {
	EnsureImage(ctx context.Context, imageRef string) error
	Run(ctx context.Context, fn FunctionSpec, window WindowRecord) (ResultRecord, error)
}

type App struct {
	cfg      Config
	store    *Store
	executor runtimeExecutor
	server   *http.Server
	slots    chan struct{}
}

func ConfigFromFlags() Config {
	cfg := Config{}

	flag.StringVar(&cfg.ListenAddr, "listen", DefaultListenAddr, "HTTP listen address")
	flag.StringVar(&cfg.StateDir, "state-dir", DefaultStateDir, "state directory")
	flag.StringVar(&cfg.ContainerdSock, "containerd-sock", "/run/containerd/containerd.sock", "containerd socket")
	flag.StringVar(&cfg.Snapshotter, "snapshotter", DefaultSnapshotter, "containerd snapshotter")
	flag.DurationVar(&cfg.PollEvery, "poll-every", DefaultPollEvery, "window polling interval")
	flag.IntVar(&cfg.MaxConcurrent, "max-concurrent", 1, "maximum concurrent function runs")
	flag.IntVar(&cfg.MaxLogBytes, "max-log-bytes", DefaultMaxLogBytes, "maximum bytes of log data returned in sync responses")
	flag.Parse()

	return cfg
}

func New(cfg Config) (*App, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.StateDir == "" {
		cfg.StateDir = DefaultStateDir
	}
	if cfg.ContainerdSock == "" {
		cfg.ContainerdSock = "/run/containerd/containerd.sock"
	}
	if cfg.Snapshotter == "" {
		cfg.Snapshotter = DefaultSnapshotter
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = DefaultPollEvery
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.MaxLogBytes <= 0 {
		cfg.MaxLogBytes = DefaultMaxLogBytes
	}

	absStateDir, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	cfg.StateDir = absStateDir

	store, err := OpenStore(cfg.StateDir)
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:      cfg,
		store:    store,
		executor: NewExecutor(cfg.ContainerdSock, cfg.StateDir, cfg.Snapshotter),
		slots:    make(chan struct{}, cfg.MaxConcurrent),
	}
	app.server = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	go a.runScheduler(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.server.Shutdown(shutdownCtx)
	}()

	log.Printf("orbitald %s listening on %s", Version, a.cfg.ListenAddr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (a *App) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.PollEvery)
	defer ticker.Stop()

	for {
		now := time.Now().UTC()
		if err := a.store.ExpireWindows(now); err != nil {
			log.Printf("expire windows: %v", err)
		}
		a.dispatchReadyWindow(ctx, now)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) dispatchReadyWindow(ctx context.Context, now time.Time) {
	select {
	case a.slots <- struct{}{}:
	default:
		return
	}

	window, ok, err := a.store.ClaimDueWindow(now)
	if err != nil {
		<-a.slots
		log.Printf("claim window: %v", err)
		return
	}
	if !ok {
		<-a.slots
		return
	}

	go func(window WindowRecord) {
		defer func() { <-a.slots }()

		fn, found := a.store.Function(window.Function)
		if !found {
			finished := time.Now().UTC()
			result := ResultRecord{
				ID:         newID("result"),
				RunID:      window.RunID,
				Function:   window.Function,
				WindowID:   window.ID,
				Area:       window.Area,
				Status:     WindowFailed,
				StartedAt:  finished,
				FinishedAt: finished,
				Error:      fmt.Sprintf("function %s not found", window.Function),
			}
			if err := a.store.CompleteWindow(window.ID, result); err != nil {
				log.Printf("complete missing function window %s: %v", window.ID, err)
			}
			return
		}

		result, err := a.executor.Run(ctx, fn, window)
		if err != nil {
			log.Printf("run %s for window %s: %v", fn.Name, window.ID, err)
		}
		if err := a.store.CompleteWindow(window.ID, result); err != nil {
			log.Printf("complete window %s: %v", window.ID, err)
		}
	}(window)
}
