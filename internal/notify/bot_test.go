package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// memSubs это подписки и kv в памяти: подменяют SQLite в тестах notify.
type memSubs struct {
	mu   sync.Mutex
	subs []string
	kv   map[string]string
}

func (m *memSubs) Subscribe(chatID string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.subs {
		if id == chatID {
			return false, nil
		}
	}
	m.subs = append(m.subs, chatID)
	return true, nil
}

func (m *memSubs) Unsubscribe(chatID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, id := range m.subs {
		if id == chatID {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *memSubs) SubscriberIDs() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.subs...), nil
}

func (m *memSubs) Get(source, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[source+"/"+key]
	return v, ok, nil
}

func (m *memSubs) Set(source, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[source+"/"+key] = value
	return nil
}

type sentMsg struct {
	chat string
	text string
}

// waitFor крутится до условия, чтобы не привязывать тест к таймингам горутины.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("условие не наступило за 2 секунды")
}

func TestBotPollHandlesCommands(t *testing.T) {
	var mu sync.Mutex
	var sends []sentMsg
	var offsets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			mu.Lock()
			offsets = append(offsets, r.Form.Get("offset"))
			first := len(offsets) == 1
			mu.Unlock()
			if first {
				w.Write([]byte(`{"ok":true,"result":[
					{"update_id":10,"message":{"chat":{"id":100},"text":"/start"}},
					{"update_id":11,"message":{"chat":{"id":200},"text":"привет"}},
					{"update_id":12,"message":{"chat":{"id":300},"text":"/stop@watch_bot"}},
					{"update_id":13,"message":{"chat":{"id":100},"text":"/start"}}]}`))
				return
			}
			w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		mu.Lock()
		sends = append(sends, sentMsg{r.PostFormValue("chat_id"), r.PostFormValue("text")})
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	store := &memSubs{}
	b := newBotAt(srv.URL, "123:abc", store)
	b.backoff = time.Millisecond

	offset, err := b.poll(context.Background(), 0)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if offset != 14 {
		t.Fatalf("курсор %d, ожидался 14", offset)
	}
	ids, _ := store.SubscriberIDs()
	if len(ids) != 1 || ids[0] != "100" {
		t.Fatalf("подписчики %v, ожидался [100]", ids)
	}
	if len(sends) != 3 {
		t.Fatalf("подтверждений %d, ожидалось 3", len(sends))
	}
	if sends[0].chat != "100" || !strings.Contains(sends[0].text, "Подписка оформлена") {
		t.Fatalf("первое подтверждение %+v", sends[0])
	}
	if sends[1].chat != "300" || !strings.Contains(sends[1].text, "Подписки не было") {
		t.Fatalf("второе подтверждение %+v", sends[1])
	}
	if sends[2].chat != "100" || !strings.Contains(sends[2].text, "уже активна") {
		t.Fatalf("третье подтверждение %+v", sends[2])
	}
	if raw, found, _ := store.Get("bot", "update_offset"); !found || raw != "14" {
		t.Fatalf("курсор в kv: %q found=%v", raw, found)
	}

	// Пустая порция курсор не двигает.
	offset, err = b.poll(context.Background(), offset)
	if err != nil {
		t.Fatalf("повторный poll: %v", err)
	}
	if offset != 14 {
		t.Fatalf("курсор после пустой порции %d, ожидался 14", offset)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offsets) != 2 || offsets[1] != "14" {
		t.Fatalf("offsets %v, ожидался повторный опрос с 14", offsets)
	}
}

func TestBotResumesFromSavedOffset(t *testing.T) {
	var mu sync.Mutex
	var offsets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		mu.Lock()
		offsets = append(offsets, r.Form.Get("offset"))
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	// Рестарт бота: курсор из kv не даёт проиграть старые команды заново.
	store := &memSubs{kv: map[string]string{"bot/update_offset": "77"}}
	b := newBotAt(srv.URL, "123:abc", store)
	b.backoff = time.Millisecond
	if _, err := b.poll(context.Background(), b.loadOffset()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offsets) != 1 || offsets[0] != "77" {
		t.Fatalf("offsets %v, ожидался опрос с 77", offsets)
	}
}

func TestBotRunStopsOnCancel(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			mu.Lock()
			polls++
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	b := newBotAt(srv.URL, "123:abc", &memSubs{})
	b.backoff = time.Millisecond
	b.pause = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return polls >= 2
	})
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

func TestBotRunSurvivesPollFailure(t *testing.T) {
	var mu sync.Mutex
	hits, oks := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			mu.Lock()
			hits++
			failing := hits <= 3
			if !failing {
				oks++
			}
			mu.Unlock()
			if failing {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	b := newBotAt(srv.URL, "123:abc", &memSubs{})
	b.backoff = time.Millisecond
	b.pause = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	// Три отказа подряд съедают повторы одного вызова, после паузы опрос
	// продолжается, а не останавливается.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return oks >= 2
	})
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

func TestBotCommandParsing(t *testing.T) {
	cases := map[string]string{
		"/start":          "/start",
		"/stop@watch_bot": "/stop",
		"  /start  ":      "/start",
		"/start привет":   "/start",
		"привет":          "привет",
		"":                "",
	}
	for text, want := range cases {
		if got := command(text); got != want {
			t.Fatalf("command(%q) = %q, ожидалось %q", text, got, want)
		}
	}
}
