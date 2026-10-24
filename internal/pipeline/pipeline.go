// Связка: прогон адаптера по расписанию, дедуп новых событий и отправка
// уведомления. Прогон без новых событий не беспокоит получателя.
package pipeline

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/dronrider/resetwatch/internal/config"
	"github.com/dronrider/resetwatch/internal/harness"
	"github.com/dronrider/resetwatch/internal/notify"
)

// Store это часть состояния, которой хватает конвейеру.
type Store interface {
	IsKnown(source, id string) (bool, error)
	MarkSeen(source, id string, at time.Time) error
	TouchOK(source string, at time.Time) error
	TouchEvents(source string, at time.Time) error
	Get(source, key string) (string, bool, error)
	Set(source, key, value string) error
}

type Pipeline struct {
	Store  Store
	Notify notify.Notifier
	HTTP   harness.HTTPClient
	RSS    harness.RSSClient
	Log    *slog.Logger
	Now    func() time.Time
}

// Run это один прогон источника. Ошибка любого шага пишется в лог и
// не роняет планировщик: следующий прогон по расписанию всё исправит.
func (p *Pipeline) Run(ctx context.Context, src config.Source) {
	if p.Now == nil {
		p.Now = time.Now
	}
	now := p.Now()
	name := src.Name()
	log := p.log().With("source", name)

	events, err := harness.Run(ctx, src.Script, harness.Deps{
		HTTP:  p.HTTP,
		RSS:   p.RSS,
		State: pipelineState{store: p.Store, source: name},
		Log:   log,
	})
	if err != nil {
		log.Error("прогон адаптера", "err", err)
		return
	}
	if err := p.Store.TouchOK(name, now); err != nil {
		log.Error("отметка свежести", "err", err)
		return
	}
	if len(events) > 0 {
		if err := p.Store.TouchEvents(name, now); err != nil {
			log.Error("отметка свежести", "err", err)
			return
		}
	}

	fresh := make([]harness.Event, 0, len(events))
	for _, e := range events {
		known, err := p.Store.IsKnown(name, e.ID)
		if err != nil {
			log.Error("проверка дедупа", "id", e.ID, "err", err)
			continue
		}
		if known {
			// Повторное событие не отправляется, но двигает last_seen.
			if err := p.Store.MarkSeen(name, e.ID, now); err != nil {
				log.Error("обновление seen", "id", e.ID, "err", err)
			}
			continue
		}
		fresh = append(fresh, e)
	}
	if len(fresh) == 0 {
		return
	}
	if err := p.Notify.Send(ctx, FormatMessage(name, fresh)); err != nil {
		log.Error("отправка уведомления", "err", err)
		return
	}
	// Событие помечается после отправки: упавшая отправка оставит его
	// новым, и следующий прогон попробует снова.
	for _, e := range fresh {
		if err := p.Store.MarkSeen(name, e.ID, now); err != nil {
			log.Error("запись seen", "id", e.ID, "err", err)
		}
	}
	log.Info("прогон", "events", len(events), "new", len(fresh))
}

// FormatMessage собирает уведомление по событиям одного прогона.
func FormatMessage(service string, events []harness.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b> (%s)\n", html.EscapeString(service), plural(len(events), "новое событие", "новых события", "новых событий"))
	for _, e := range events {
		b.WriteString("\n<b>")
		b.WriteString(html.EscapeString(kindTitle(e.Kind)))
		b.WriteString("</b>: ")
		b.WriteString(html.EscapeString(e.Title))
		b.WriteString("\n")
		if t, err := time.Parse(time.RFC3339, e.Time); err == nil {
			// Все времена приводятся к UTC, чтобы события разных источников
			// сравнивались в одной зоне.
			b.WriteString(t.UTC().Format("02.01.2006 15:04 UTC"))
		} else {
			b.WriteString(e.Time)
		}
		if e.URL != "" {
			b.WriteString("\n")
			b.WriteString(html.EscapeString(e.URL))
		}
		if e.Note != "" {
			b.WriteString("\n")
			b.WriteString(html.EscapeString(e.Note))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func kindTitle(kind string) string {
	switch kind {
	case "hint":
		return "намёк"
	case "promise":
		return "обещание"
	case "reset":
		return "сброс"
	case "limit_raise":
		return "поднятие лимита"
	case "forecast":
		return "прогноз"
	}
	return kind
}

func plural(n int, one, few, many string) string {
	if n%10 == 1 && n%100 != 11 {
		return fmt.Sprintf("%d %s", n, one)
	}
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20) {
		return fmt.Sprintf("%d %s", n, few)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// pipelineState пришивает ключи адаптера к его источнику.
type pipelineState struct {
	store  Store
	source string
}

func (s pipelineState) Get(key string) (string, bool, error) {
	return s.store.Get(s.source, key)
}

func (s pipelineState) Set(key, value string) error {
	return s.store.Set(s.source, key, value)
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}
