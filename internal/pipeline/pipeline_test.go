package pipeline

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/dronrider/resetwatch/internal/config"
	"github.com/dronrider/resetwatch/internal/harness"
	"github.com/dronrider/resetwatch/internal/state"
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

func TestDedupSendsOnce(t *testing.T) {
	notifier := &fakeNotifier{}
	pipe, _ := newPipeline(t, notifier)
	ctx := context.Background()
	pipe.Run(ctx, echoSource())
	pipe.Run(ctx, echoSource())
	if len(notifier.sent) != 1 {
		t.Fatalf("после двух прогонов отправлено %d уведомлений, ожидалось 1", len(notifier.sent))
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
	got := FormatMessage("clauderesets", events)
	want := `<b>clauderesets</b> (2 новых события)

<b>сброс</b>: Сброс &lt;лимитов&gt;
20.10.2026 00:00 UTC
https://example.test/reset
обнуление

<b>прогноз</b>: Прогноз на &lt;понедельник&gt;
21.10.2026 09:30 UTC`
	if got != want {
		t.Fatalf("формат:\nполучено:\n%s\nожидалось:\n%s", got, want)
	}
}

func TestKindTitlesAndPlural(t *testing.T) {
	if got := kindTitle("limit_raise"); got != "поднятие лимита" {
		t.Fatalf("kindTitle: %s", got)
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
