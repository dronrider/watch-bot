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

func TestTelegramSendOK(t *testing.T) {
	var mu sync.Mutex
	var forms []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if !strings.Contains(r.URL.Path, "/bot123:abc/sendMessage") {
			t.Errorf("путь: %s", r.URL.Path)
		}
		mu.Lock()
		forms = append(forms, r.Form.Encode())
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	tg := newTelegramAt(srv.URL, "123:abc", "42")
	tg.backoff = time.Millisecond
	if err := tg.Send(context.Background(), "<b>привет</b>"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(forms) != 1 {
		t.Fatalf("запросов %d", len(forms))
	}
	for _, want := range []string{"chat_id=42", "parse_mode=HTML"} {
		if !strings.Contains(forms[0], want) {
			t.Fatalf("форма без %s: %s", want, forms[0])
		}
	}
}

func TestTelegramRetriesOnServerError(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := newTelegramAt(srv.URL, "123:abc", "42")
	tg.backoff = time.Millisecond
	if err := tg.Send(context.Background(), "текст"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hits != 3 {
		t.Fatalf("попыток %d, ожидалось 3", hits)
	}
}

func TestTelegramFromEnv(t *testing.T) {
	t.Setenv(envToken, "")
	t.Setenv(envChatID, "")
	if _, err := TelegramFromEnv(); err == nil {
		t.Fatal("без переменных окружения ожидалась ошибка")
	}
}
