// Сторож свежести: источник, который давно не отрабатывал или не давал
// событий, сам сообщает о себе молчанием.
package watchdog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dronrider/resetwatch/internal/config"
	"github.com/dronrider/resetwatch/internal/notify"
)

const silentKey = "watchdog:silent_notified"

// Store это часть состояния, которой хватает сторожу.
type Store interface {
	Fresh(source string) (lastOK, lastEvents time.Time, err error)
	Get(source, key string) (string, bool, error)
	Set(source, key, value string) error
}

type Watchdog struct {
	Store  Store
	Notify notify.Notifier
	Log    *slog.Logger
	Now    func() time.Time
}

// Check обходит источники конфига. Выключенные пропускает: раз источник
// выключен, его молчание штатно. Уведомление о молчании отправляется не
// чаще раза в сутки, отметка лежит в kv самого источника.
func (w *Watchdog) Check(ctx context.Context, sources []config.Source) {
	if w.Now == nil {
		w.Now = time.Now
	}
	today := w.Now().UTC().Format("2006-01-02")
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if err := w.checkSource(ctx, src, today); err != nil {
			w.log().Error("сторож", "source", src.Name(), "err", err)
		}
	}
}

func (w *Watchdog) checkSource(ctx context.Context, src config.Source, today string) error {
	name := src.Name()
	lastOK, lastEvents, err := w.Store.Fresh(name)
	if err != nil {
		return err
	}
	// Свежесть это максимум из последнего успеха и последнего события:
	// источник без ошибок, но без событий, всё ещё дышит.
	last := lastOK
	if lastEvents.After(last) {
		last = lastEvents
	}
	stale := last.IsZero()
	reason := "ни одного успешного прогона"
	if stale {
		reason = "ни одного успешного прогона"
	} else if w.Now().After(last.AddDate(0, 0, src.FreshnessDays)) {
		stale = true
		reason = fmt.Sprintf("последняя активность %s", last.Format("02.01.2006"))
	}
	if !stale {
		w.Store.Set(name, silentKey, "")
		return nil
	}
	marked, found, err := w.Store.Get(name, silentKey)
	if err != nil {
		return err
	}
	if found && marked == today {
		return nil
	}
	text := fmt.Sprintf("источник молчит: %s (%s), порог %d дн.", name, reason, src.FreshnessDays)
	if err := w.Notify.Send(ctx, text); err != nil {
		return err
	}
	return w.Store.Set(name, silentKey, today)
}

func (w *Watchdog) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}
