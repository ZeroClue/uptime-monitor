package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ZeroClue/uptime-monitor/internal/alerting"
	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
	"github.com/ZeroClue/uptime-monitor/internal/dashboard"
	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/ssh"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "/config", "Path to config directory")
	dataPath := flag.String("data", "/data", "Path to data directory")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	}))
	slog.SetDefault(logger)

	slog.Info("starting uptime monitor", "version", version, "buildTime", buildTime)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutdown signal received")
		cancel()
	}()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	db, err := storage.New(*dataPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		slog.Error("failed to migrate database", "error", err)
		os.Exit(1)
	}

	if err := db.EnsureDefaultProject(ctx); err != nil {
		slog.Error("failed to ensure default project", "error", err)
		os.Exit(1)
	}

	if err := db.SeedHosts(cfg.Hosts); err != nil {
		slog.Error("failed to seed hosts", "error", err)
		os.Exit(1)
	}

	sshClient := ssh.NewSSHClient(logger, nil)

	collectorChain := collector.NewChain(
		collector.NewLocalProcfsCollector(collector.WithLocalLogger(logger)),
		collector.NewPsutilCollector(collector.WithPsutilSSHClient(sshClient)),
		collector.NewProcfsCollector(collector.WithProcfsSSHClient(sshClient)),
		collector.NewTailscaleCollector(collector.WithTailscaleSSHClient(sshClient)),
	)

	sched := scheduler.NewWithRetry(cfg.PollInterval, db, collectorChain, logger, cfg.Retry)
	go sched.Run(ctx)

	alertEngine := alerting.NewEngine(db, sched, logger)
	configDir := *configPath
	if err := alertEngine.LoadFromConfig(filepath.Join(configDir, "thresholds.yaml")); err != nil {
		slog.Warn("failed to load alert config", "error", err)
	}
	go alertEngine.Run(ctx)

	cookieSecure := os.Getenv("COOKIE_SECURE") == "true"
	dashboardServer := dashboard.NewServer(cfg.DashboardPassword, db, sched, logger, cookieSecure)
	go dashboardServer.Run(ctx)

	<-ctx.Done()
	slog.Info("shutdown complete")
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
