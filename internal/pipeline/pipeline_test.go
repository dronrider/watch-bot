package pipeline

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/watch-bot/internal/config"
	"github.com/dronrider/watch-bot/internal/harness"
	"github.com/dronrider/watch-bot/internal/state"
)

type fakeNotifier struct {
	sent []string
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

type deadHTTP struct{}

func (deadHTTP) Get(ctx context.Context, url string, _ harness.GetOptions) (harness.Response, error) {
	return harness.Response{}, nil
}

func newPipeline(t *testing.T, notifier *fakeNotifier) (*Pipeline, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &Pipeline{
		Store:  store,
		Notify: notifier,
		HTTP:   deadHTTP{},
		RSS:    nil,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) },
	}, store
}

func echoSource() config.Source {
	return config.Source{Script: "testdata/echo.js", Enabled: true}
}

func TestColdStartSeedsSilently(t *testing.T) {
	notifier := &fakeNotifier{}
	pipe, store := newPipeline(t, notifier)
	ctx := context.Background()
	pipe.Run(ctx, echoSource())
	pipe.Run(ctx, echoSource())
	if len(notifier.sent) != 0 {
		t.Fatalf("холодный старт отправил %d уведомлений, ожидалось 0", len(notifier.sent))
	}
	known, err := store.IsKnown("echo", "echo:1")
	if err != nil || !known {
		t.Fatalf("событие не заложено в базу: known=%v err=%v", known, err)
	}
}

func TestNewEventAfterSeedSendsOnce(t *testing.T) {
	notifier := &fakeNotifier{}
	pipe, store := newPipeline(t, notifier)
	ctx := context.Background()
	pipe.Run(ctx, echoSource())
	if err := store.Set("echo", "mode", "1"); err != nil {
		t.Fatal(err)
	}
	pipe.Run(ctx, echoSource())
	if len(notifier.sent) != 1 {
		t.Fatalf("отправлено %d уведомлений, ожидалось 1", len(notifier.sent))
	}
	if !strings.Contains(notifier.sent[0], "Обещание сброса") {
		t.Fatalf("в отправке нет нового события: %s", notifier.sent[0])
	}
}

func TestRunMarksFreshness(t *testing.T) {
	notifier := &fakeNotifier{}
	pipe, store := newPipeline(t, notifier)
	pipe.Run(context.Background(), echoSource())
	ok, events, err := store.Fresh("echo")
	if err != nil {
		t.Fatal(err)
	}
	if ok.IsZero() || events.IsZero() {
		t.Fatalf("свежесть не отмечена: ok=%v events=%v", ok, events)
	}
}

func TestFormatMessage(t *testing.T) {
	events := []harness.Event{
		{
			ID:    "x:1",
			Title: "Сброс <лимитов>",
			Time:  "2026-10-20T00:00:00Z",
			Kind:  "reset",
			URL:   "https://example.test/reset",
			Note:  "обнуление",
		},
		{
			ID:    "x:2",
			Title: "Прогноз на <понедельник>",
			Time:  "2026-10-21T12:30:00+03:00",
			Kind:  "forecast",
			URL:   "",
			Note:  "",
		},
	}
	src := config.Source{Script: "adapters/clauderesets.js", Service: "Claude"}
	got := FormatMessage(src, events)
	want := strings.Join([]string{
		"<b>" + emojiRobot + " Claude: 2 новых события</b>",
		"",
		emojiParty + " Лимиты сброшены",
		"Лимиты обнулились, счётчик пошёл с нуля. Можно возвращаться к работе.",
		"«Сброс &lt;лимитов&gt;»",
		emojiClock + " 20.10.2026 00:00 UTC",
		emojiLink + " https://example.test/reset",
		emojiInfo + " обнуление",
		"",
		emojiCrystal + " Прогноз сброса",
		"Сброс спрогнозирован по накопленным сигналам. Это оценка, а не обещание.",
		"«Прогноз на &lt;понедельник&gt;»",
		emojiClock + " 21.10.2026 09:30 UTC",
	}, "\n")
	if got != want {
		t.Fatalf("формат:\nполучено:\n%s\nожидалось:\n%s", got, want)
	}
}

func TestFormatMessageSingleEvent(t *testing.T) {
	src := config.Source{Script: "adapters/codexresetdev.js", Service: "Codex"}
	events := []harness.Event{
		{ID: "x:1", Title: "Reset recorded", Time: "2026-10-20T15:04:00Z", Kind: "reset", URL: "https://x.test/1"},
	}
	got := FormatMessage(src, events)
	want := strings.Join([]string{
		"<b>" + emojiParty + " Codex: лимиты сброшены</b>",
		"",
		"Лимиты обнулились, счётчик пошёл с нуля. Можно возвращаться к работе.",
		"«Reset recorded»",
		emojiClock + " 20.10.2026 15:04 UTC",
		emojiLink + " https://x.test/1",
	}, "\n")
	if got != want {
		t.Fatalf("формат:\nполучено:\n%s\nожидалось:\n%s", got, want)
	}
}

func TestFormatMessageFallsBackToSourceName(t *testing.T) {
	src := config.Source{Script: "adapters/modelyard.js"}
	events := []harness.Event{{ID: "x:1", Title: "Hint", Time: "2026-10-20T00:00:00Z", Kind: "hint"}}
	got := FormatMessage(src, events)
	want := "<b>" + emojiEyes + " modelyard: признак скорого сброса</b>\n\n" +
		"Трекер заметил намёк на приближающийся сброс. Подтверждения пока нет.\n" +
		"«Hint»\n" + emojiClock + " 20.10.2026 00:00 UTC"
	if got != want {
		t.Fatalf("формат:\nполучено:\n%s\nожидалось:\n%s", got, want)
	}
}

func TestCrossKey(t *testing.T) {
	cases := []struct {
		name string
		ev   harness.Event
		want string
	}{
		{"ссылка в url", harness.Event{URL: "https://x.com/thsottiaux/status/2114237083652882932"}, "xref:2114237083652882932"},
		{"старый домен в заметке", harness.Event{Note: "Source: https://twitter.com/thsottiaux/status/123"}, "xref:123"},
		{"чужая ссылка", harness.Event{URL: "https://tibo.modelyard.dev/events/132"}, ""},
		{"без ссылок", harness.Event{}, ""},
	}
	for _, c := range cases {
		if got := crossKey(c.ev); got != c.want {
			t.Fatalf("%s: %s, ожидалось %s", c.name, got, c.want)
		}
	}
}

func TestCrossSourceDuplicateSilenced(t *testing.T) {
	notifier := &fakeNotifier{}
	pipe, store := newPipeline(t, notifier)
	ctx := context.Background()
	first := config.Source{Script: "testdata/dupx.js", Enabled: true}
	second := config.Source{Script: "testdata/dupx2.js", Enabled: true}
	// Холодный старт обоих источников: ленты пустые, ключей нет.
	pipe.Run(ctx, first)
	pipe.Run(ctx, second)
	if len(notifier.sent) != 0 {
		t.Fatalf("холодный старт отправил %d уведомлений", len(notifier.sent))
	}
	// Свежий пост приходит в обе ленты: первый источник сообщает, второй
	// молчит, потому что пост уже прозвучал.
	if err := store.Set("dupx", "mode", "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("dupx2", "mode", "1"); err != nil {
		t.Fatal(err)
	}
	pipe.Run(ctx, first)
	pipe.Run(ctx, second)
	if len(notifier.sent) != 1 {
		t.Fatalf("пересказ одного поста: отправлено %d уведомлений, ожидалось 1", len(notifier.sent))
	}
	if !strings.Contains(notifier.sent[0], "fully propagated") {
		t.Fatalf("отправлено сообщение не первого источника: %s", notifier.sent[0])
	}
	// Пересказ всё равно заложен в seen: повторных попыток не будет.
	known, err := store.IsKnown("dupx2", "dupx2:1")
	if err != nil || !known {
		t.Fatalf("пересказ не заложен в базу: known=%v err=%v", known, err)
	}
}

func TestColdStartSeedsCrossKey(t *testing.T) {
	notifier := &fakeNotifier{}
	pipe, store := newPipeline(t, notifier)
	if err := store.Set("dupx", "mode", "1"); err != nil {
		t.Fatal(err)
	}
	pipe.Run(context.Background(), config.Source{Script: "testdata/dupx.js", Enabled: true})
	if len(notifier.sent) != 0 {
		t.Fatalf("холодный старт отправил %d уведомлений", len(notifier.sent))
	}
	if _, ok, _ := store.Get(globalSource, "xref:2114237083652882932"); !ok {
		t.Fatal("холодный старт не заложил сквозной ключ поста")
	}
}

func TestKindTextsAndPlural(t *testing.T) {
	if got := kindText("limit_raise").lead; got != "лимиты подняты" {
		t.Fatalf("kindText: %s", got)
	}
	// Вид вне словаря откатывается к самому себе.
	if got := kindText("odd").lead; got != "odd" {
		t.Fatalf("kindText вне словаря: %s", got)
	}
	if got := plural(1, "событие", "события", "событий"); got != "1 событие" {
		t.Fatalf("plural 1: %s", got)
	}
	if got := plural(3, "событие", "события", "событий"); got != "3 события" {
		t.Fatalf("plural 3: %s", got)
	}
	if got := plural(11, "событие", "события", "событий"); got != "11 событий" {
		t.Fatalf("plural 11: %s", got)
	}
}
