// Command server runs the REST API and the cron scheduler.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiabee/game-scheduler/internal/api"
	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/game/hsr"
	"github.com/xiabee/game-scheduler/internal/game/r1999"
	"github.com/xiabee/game-scheduler/internal/game/wuwa"
	"github.com/xiabee/game-scheduler/internal/monitor"
	"github.com/xiabee/game-scheduler/internal/notify"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
	"github.com/xiabee/game-scheduler/internal/version"
)

func main() {
	os.Exit(run())
}

// run keeps every cleanup in defers: a plain os.Exit from deep inside would
// skip them and leave running tool processes orphaned and the store open.
func run() int {
	cfgPath := flag.String("config", "", "path to JSON config file (optional)")
	addr := flag.String("addr", "", "HTTP listen address override")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		println("game-scheduler server " + version.Version)
		return 0
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		return 1
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	if err := cfg.EnsureDirs(); err != nil {
		log.Error("ensure dirs", "err", err)
		return 1
	}

	// Many supported tools (e.g. BetterGI) must run with administrator rights to
	// simulate input into an elevated game; a child process can only inherit
	// elevation, so the scheduler itself has to be elevated. Warn early — this is
	// the usual cause of BetterGI exit code 553.
	if elevated, known := isElevated(); known && !elevated {
		log.Warn("not running as Administrator: tools that control the game (e.g. BetterGI) may fail with exit code 553; relaunch via examples/run-admin.ps1")
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open store", "err", err)
		return 1
	}
	defer st.Close()

	// Reconcile executions left in flight by a previous crash/restart; their
	// processes and cancel handles are gone, so they can never finish.
	if n, err := st.RecoverOrphans(); err != nil {
		log.Warn("recover orphaned executions", "err", err)
	} else if n > 0 {
		log.Info("recovered orphaned executions", "count", n)
	}

	bus := events.New()
	notifier := notify.New(cfg.NotifyCmd, log)
	reg := game.NewRegistry(genshin.New(), hsr.New(), wuwa.New(), r1999.New())
	svc := task.NewService(st, reg, cfg, bus, log)
	svc.SetNotify(notifier.Send)
	// Drain in-flight task workers before the deferred st.Close() runs (defers
	// are LIFO, so registering this after st.Close keeps the order: scheduler
	// stop -> drain executions -> close store).
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		svc.Shutdown(ctx)
	}()

	// Resource monitor: live CPU/RAM sampling + optional overload gating.
	monCtx, monCancel := context.WithCancel(context.Background())
	defer monCancel()
	mon := monitor.New(monitor.Config{
		Enabled:      cfg.MonitorEnabled,
		CPUThreshold: cfg.CPUThreshold,
		MemThreshold: cfg.MemThreshold,
		Interval:     time.Duration(cfg.MonitorIntervalSec) * time.Second,
		Policy:       cfg.OverloadPolicy,
		DiskPath:     cfg.DataDir,
	}, nil, bus, log)
	mon.SetNotify(notifier.Send)
	mon.Start(monCtx)

	sched := scheduler.New(st, svc, log)
	sched.SetPauseGate(mon.ShouldPause)
	if err := sched.Start(); err != nil {
		log.Error("start scheduler", "err", err)
		return 1
	}
	defer sched.Stop()

	// Execution-log retention: delete finished executions older than the
	// configured window (default 30 days) so the database does not grow
	// without bound. Runs once at startup and then every 6 hours.
	if cfg.ExecutionRetentionDays > 0 {
		prune := func() {
			cutoff := time.Now().UTC().AddDate(0, 0, -cfg.ExecutionRetentionDays)
			if n, err := st.PruneExecutions(cutoff); err != nil {
				log.Warn("prune executions", "err", err)
			} else if n > 0 {
				log.Info("pruned old executions", "count", n, "retention_days", cfg.ExecutionRetentionDays)
			}
		}
		go func() {
			prune()
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-monCtx.Done():
					return
				case <-ticker.C:
					prune()
				}
			}
		}()
	}

	apiSrv := api.New(st, svc, sched, reg, bus, mon, cfg, log)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Release SSE clients on shutdown: without this, Shutdown waits out its
	// whole timeout on every open dashboard event stream.
	srv.RegisterOnShutdown(apiSrv.ShutdownStreams)

	serverErr := make(chan error, 1)
	go func() {
		log.Info("server listening", "addr", cfg.Addr, "db", cfg.DBPath, "adapters", reg.Keys(), "version", version.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("listen on %s: %w", cfg.Addr, err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serverErr:
		// Return (not os.Exit) so the defers above still drain workers and
		// close the store cleanly.
		log.Error("http server", "err", err)
		return 1
	case <-stop:
	}
	log.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown", "err", err)
	}
	return 0
}
