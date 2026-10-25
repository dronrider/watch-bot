// Связка: прогон адаптера по расписанию, дедуп новых событий и отправка
// уведомления. Прогон без новых событий не беспокоит получателя.
package pipeline

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dronrider/watch-bot/internal/config"
	"github.com/dronrider/watch-bot/internal/harness"
	"github.com/dronrider/watch-bot/internal/notify"
)

// Store это часть состояния, которой хватает конвейеру.
type Store interface {
	IsKnown(source, id string) (bool, error)
	MarkSeen(source, id string, at time.Time) error
	TouchOK(source string, at time.Time) error
	TouchEvents(source string, at time.Time) error
	Fresh(source string) (lastOK, lastEvents time.Time, err error)
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
	lastOK, _, err := p.Store.Fresh(name)
	if err != nil {
		log.Error("чтение свежести", "err", err)
		return
	}
	first := lastOK.IsZero()
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
	if first {
		// Холодный старт: пустая база сочла бы новыми всю историю
		// источника, поэтому первый прогон закладывает события молча.
		for _, e := range events {
			if err := p.Store.MarkSeen(name, e.ID, now); err != nil {
				log.Error("запись seen", "id", e.ID, "err", err)
			}
			p.markCross(log, e, now)
		}
		if len(events) > 0 {
			log.Info("первый прогон: события заложены без уведомления", "events", len(events))
		}
		return
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
	// Сквозной дедуп: трекеры пересказывают одни и те же посты, и пересказ
	// уже сообщённого поста молчит. Холодный старт закладывает ключи так же
	// молча, поэтому история другого источника не приходит сообщением.
	var send []harness.Event
	reported := map[string]bool{}
	for _, e := range fresh {
		key := crossKey(e)
		if key != "" {
			if reported[key] {
				if err := p.Store.MarkSeen(name, e.ID, now); err != nil {
					log.Error("запись seen", "id", e.ID, "err", err)
				}
				log.Info("пост дважды в прогоне, второй пересказ молчит", "id", e.ID)
				continue
			}
			_, known, err := p.Store.Get(globalSource, key)
			if err != nil {
				log.Error("чтение сквозного дедупа", "key", key, "err", err)
			} else if known {
				if err := p.Store.MarkSeen(name, e.ID, now); err != nil {
					log.Error("запись seen", "id", e.ID, "err", err)
				}
				log.Info("пост уже сообщён другим источником, пересказ молчит", "id", e.ID)
				continue
			}
			reported[key] = true
		}
		send = append(send, e)
	}
	if len(send) == 0 {
		return
	}
	if err := p.Notify.Send(ctx, FormatMessage(src, send)); err != nil {
		log.Error("отправка уведомления", "err", err)
		return
	}
	// Событие помечается после отправки: упавшая отправка оставит его
	// новым, и следующий прогон попробует снова.
	for _, e := range send {
		if err := p.Store.MarkSeen(name, e.ID, now); err != nil {
			log.Error("запись seen", "id", e.ID, "err", err)
		}
		p.markCross(log, e, now)
	}
	log.Info("прогон", "events", len(events), "new", len(send))
}

// globalSource это псевдоисточник для сквозных ключей: записи, общие всем
// источникам сразу, а не одному.
const globalSource = ""

// statusLink ловит ссылку на пост в X: она исходник для трекеров, и два
// трекера, пересказывающие один пост, это одно событие.
var statusLink = regexp.MustCompile(`https?://(?:www\.)?(?:x|twitter)\.com/[A-Za-z0-9_]{1,20}/status/(\d+)`)

// crossKey даёт сквозной ключ события по ссылке на пост в URL или заметке.
// Пустой ключ значит, что событие ни на какой пост не ссылается.
func crossKey(e harness.Event) string {
	for _, text := range []string{e.URL, e.Note} {
		if m := statusLink.FindStringSubmatch(text); m != nil {
			return "xref:" + m[1]
		}
	}
	return ""
}

// markCross закладывает сквозной ключ события, если событие ссылается на пост.
func (p *Pipeline) markCross(log *slog.Logger, e harness.Event, now time.Time) {
	key := crossKey(e)
	if key == "" {
		return
	}
	if err := p.Store.Set(globalSource, key, now.Format(time.RFC3339)); err != nil {
		log.Error("запись сквозного дедупа", "key", key, "err", err)
	}
}

// Эмодзи уведомлений записаны эскейпами юникода: исходник держится в
// символах клавиатуры en/ru.
const (
	emojiParty    = "\U0001F389" // хлопушки
	emojiCalendar = "\U0001F4C5" // календарь
	emojiEyes     = "\U0001F440" // глаза
	emojiChart    = "\U0001F4C8" // растущий график
	emojiCrystal  = "\U0001F52E" // хрустальный шар
	emojiRobot    = "\U0001F916" // робот
	emojiLaptop   = "\U0001F4BB" // ноутбук
	emojiBell     = "\U0001F514" // колокольчик
	emojiClock    = "\U0001F552" // циферблат
	emojiLink     = "\U0001F517" // звено цепи
	emojiInfo     = "\U0001F4AC" // облачко с репликой
)

// kindInfo это подача вида события: эмодзи, строка-заголовок и фраза,
// объясняющая, что случилось и как на это реагировать.
type kindInfo struct {
	emoji  string
	lead   string
	action string
}

func kindText(kind string) kindInfo {
	switch kind {
	case "hint":
		return kindInfo{emojiEyes, "признак скорого сброса", "Трекер заметил намёк на приближающийся сброс. Подтверждения пока нет."}
	case "promise":
		return kindInfo{emojiCalendar, "обещан сброс лимитов", "Сброс ещё не случился, но его пообещали. Подтверждение придёт отдельным сообщением."}
	case "reset":
		return kindInfo{emojiParty, "лимиты сброшены", "Лимиты обнулились, счётчик пошёл с нуля. Можно возвращаться к работе."}
	case "limit_raise":
		return kindInfo{emojiChart, "лимиты подняты", "Базовые лимиты стали выше, запас в работе вырос."}
	case "forecast":
		return kindInfo{emojiCrystal, "прогноз сброса", "Сброс спрогнозирован по накопленным сигналам. Это оценка, а не обещание."}
	}
	return kindInfo{emojiBell, kind, "Событие из ленты трекера."}
}

// serviceTitle превращает поле service в имя продукта и эмодзи для заголовка.
func serviceTitle(src config.Source) (string, string) {
	switch strings.ToLower(src.Service) {
	case "claude":
		return "Claude", emojiRobot
	case "codex":
		return "Codex", emojiLaptop
	}
	if src.Service != "" {
		return src.Service, emojiBell
	}
	return src.Name(), emojiBell
}

// upperFirst поднимает первую букву строки, чтобы заголовок блока читался
// самостоятельной строкой.
func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// FormatMessage собирает уведомление по событиям одного прогона. Заголовок
// называет продукт и главное происшествие, каждое событие объясняет, что
// случилось и как на это реагировать.
func FormatMessage(src config.Source, events []harness.Event) string {
	name, product := serviceTitle(src)
	var b strings.Builder
	if len(events) == 1 {
		k := kindText(events[0].Kind)
		fmt.Fprintf(&b, "<b>%s %s: %s</b>\n", k.emoji, name, k.lead)
	} else {
		fmt.Fprintf(&b, "<b>%s %s: %s</b>\n", product, name, plural(len(events), "новое событие", "новых события", "новых событий"))
	}
	for i, e := range events {
		k := kindText(e.Kind)
		if i > 0 {
			// Между событиями вставляется пустая строка, чтобы блоки
			// читались раздельно.
			b.WriteString("\n")
		}
		b.WriteString("\n")
		if len(events) > 1 {
			fmt.Fprintf(&b, "%s %s\n", k.emoji, upperFirst(k.lead))
		}
		b.WriteString(k.action)
		b.WriteString("\n«")
		b.WriteString(html.EscapeString(e.Title))
		b.WriteString("»\n")
		if t, err := time.Parse(time.RFC3339, e.Time); err == nil {
			// Все времена приводятся к UTC, чтобы события разных источников
			// сравнивались в одной зоне.
			b.WriteString(emojiClock + " " + t.UTC().Format("02.01.2006 15:04 UTC"))
		} else {
			b.WriteString(emojiClock + " " + e.Time)
		}
		if e.URL != "" {
			b.WriteString("\n" + emojiLink + " " + html.EscapeString(e.URL))
		}
		if e.Note != "" {
			b.WriteString("\n" + emojiInfo + " " + html.EscapeString(e.Note))
		}
	}
	return strings.TrimRight(b.String(), "\n")
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
