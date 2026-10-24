// resetwatch это монитор сбросов лимитов: по расписанию опрашивает
// источники, дедупит события и присылает уведомления в Telegram.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/dronrider/watch-bot/internal/config"
	"github.com/dronrider/watch-bot/internal/harness"
	"github.com/dronrider/watch-bot/internal/notify"
	"github.com/dronrider/watch-bot/internal/pipeline"
	"github.com/dronrider/watch-bot/internal/state"
	"github.com/dronrider/watch-bot/internal/watchdog"
)

// runTimeout режет зависший прогон адаптера, чтобы cron не копил задания.
const runTimeout = 5 * time.Minute

// watchdogSchedule гоняет сторож свежести каждый час.
const watchdogSchedule = "0 * * * *"

func main() {
	configPath := flag.String("config", "configs/config.toml", "путь к конфигурации")
	once := flag.Bool("once", false, "прогнать источники один раз и выйти")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*configPath, *once, log); err != nil {
		log.Error("остановка", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, once bool, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	notifier, err := notify.TelegramFromEnv()
	if err != nil {
		return err
	}
	store, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	httpClient := harness.NewRealHTTP()
	pipe := &pipeline.Pipeline{
		Store:  store,
		Notify: notifier,
		HTTP:   httpClient,
		RSS:    harness.NewRealRSS(httpClient),
		Log:    log,
	}
	dog := &watchdog.Watchdog{
		Store:  store,
		Notify: notifier,
		Log:    log,
	}

	// Разовый прогон это дым и проверка руками: тика планировщика он не ждёт.
	if once {
		for _, src := range cfg.Sources {
			if !src.Enabled {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
			defer cancel()
			pipe.Run(ctx, src)
		}
		dctx, dcancel := context.WithTimeout(context.Background(), runTimeout)
		defer dcancel()
		dog.Check(dctx, cfg.Sources)
		return nil
	}

	scheduler := cron.New()
	for _, src := range cfg.Sources {
		if !src.Enabled {
			log.Info("источник выключен", "source", src.Name())
			continue
		}
		src := src
		if _, err := scheduler.AddFunc(src.Schedule, func() {
			ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
			defer cancel()
			pipe.Run(ctx, src)
		}); err != nil {
			return err
		}
		log.Info("источник в расписании", "source", src.Name(), "schedule", src.Schedule)
	}
	if _, err := scheduler.AddFunc(watchdogSchedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		dog.Check(ctx, cfg.Sources)
	}); err != nil {
		return err
	}

	scheduler.Start()
	log.Info("запуск", "config", configPath, "sources", len(cfg.Sources))
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	<-scheduler.Stop().Done()
	return nil
}
