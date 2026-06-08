// Command server runs the REST API and the cron scheduler.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiabee/game-scheduler/internal/api"
	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/game/hsr"
	"github.com/xiabee/game-scheduler/internal/game/r1999"
	"github.com/xiabee/game-scheduler/internal/game/wuwa"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
)

func main() {
	cfgPath := flag.String("config", "", "path to JSON config file (optional)")
	addr := flag.String("addr", "", "HTTP listen address override")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	if err := cfg.EnsureDirs(); err != nil {
		log.Error("ensure dirs", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	reg := game.NewRegistry(genshin.New(), hsr.New(), wuwa.New(), r1999.New())
	svc := task.NewService(st, reg, cfg, log)
	sched := scheduler.New(st, svc, log)
	if err := sched.Start(); err != nil {
		log.Error("start scheduler", "err", err)
		os.Exit(1)
	}
	defer sched.Stop()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(st, svc, sched, reg, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("server listening", "addr", cfg.Addr, "db", cfg.DBPath, "adapters", reg.Keys())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown", "err", err)
	}
}
