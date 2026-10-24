package watchdog

import (
	"context"
	"testing"
	"time"

	"github.com/dronrider/watch-bot/internal/config"
)

type fakeStore struct {
	freshOK, freshEvents time.Time
	kv                   map[string]string
}

func (f *fakeStore) Fresh(string) (time.Time, time.Time, error) {
	return f.freshOK, f.freshEvents, nil
}

func (f *fakeStore) Get(_ string, key string) (string, bool, error) {
	v, ok := f.kv[key]
	return v, ok, nil
}

func (f *fakeStore) Set(_ string, key, value string) error {
	if f.kv == nil {
		f.kv = map[string]string{}
	}
	f.kv[key] = value
	return nil
}

type fakeNotifier struct {
	sent []string
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newDog(store *fakeStore, notifier *fakeNotifier) *Watchdog {
	return &Watchdog{Store: store, Notify: notifier, Now: func() time.Time { return now }}
}

func enabled(name string) config.Source {
	return config.Source{Script: "adapters/" + name + ".js", Enabled: true, FreshnessDays: 3}
}

func TestSilentSourceNotifies(t *testing.T) {
	store := &fakeStore{}
	notifier := &fakeNotifier{}
	newDog(store, notifier).Check(context.Background(), []config.Source{enabled("clauderesets")})
	if len(notifier.sent) != 1 {
		t.Fatalf("ожидалось одно уведомление, получено %d", len(notifier.sent))
	}
	want := "источник молчит: clauderesets (ни одного успешного прогона), порог 3 дн."
	if notifier.sent[0] != want {
		t.Fatalf("текст: %q", notifier.sent[0])
	}
	// Второй прогон того же дня молчит: отметка уже стоит.
	newDog(store, notifier).Check(context.Background(), []config.Source{enabled("clauderesets")})
	if len(notifier.sent) != 1 {
		t.Fatalf("повтор за сутки: отправлено %d", len(notifier.sent))
	}
}

func TestFreshSourceSilent(t *testing.T) {
	store := &fakeStore{freshOK: now.Add(-time.Hour)}
	notifier := &fakeNotifier{}
	newDog(store, notifier).Check(context.Background(), []config.Source{enabled("clauderesets")})
	if len(notifier.sent) != 0 {
		t.Fatalf("свежий источник: отправлено %d", len(notifier.sent))
	}
}

func TestStaleSourceNotifiesOnceADay(t *testing.T) {
	store := &fakeStore{freshOK: now.AddDate(0, 0, -5)}
	notifier := &fakeNotifier{}
	newDog(store, notifier).Check(context.Background(), []config.Source{enabled("modelyard")})
	if len(notifier.sent) != 1 {
		t.Fatalf("постаревший источник: отправлено %d", len(notifier.sent))
	}
	if store.kv[silentKey] != "2026-10-24" {
		t.Fatalf("отметка суток: %+v", store.kv)
	}
}

func TestDisabledSourceSkipped(t *testing.T) {
	store := &fakeStore{}
	notifier := &fakeNotifier{}
	disabled := config.Source{Script: "adapters/codexresetdev.js", Enabled: false, FreshnessDays: 3}
	newDog(store, notifier).Check(context.Background(), []config.Source{disabled})
	if len(notifier.sent) != 0 {
		t.Fatalf("выключенный источник: отправлено %d", len(notifier.sent))
	}
}
