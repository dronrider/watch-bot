package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
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

func TestSplitKeepsRunesWhole(t *testing.T) {
	// Строка из четырёхбайных эмодзи длиннее лимита: рез обязан пройти по
	// границе руна.
	line := strings.Repeat("\U0001F389", 1000)
	parts := splitMessage(line)
	if len(parts) < 2 {
		t.Fatalf("ожидалась резка, частей %d", len(parts))
	}
	for i, part := range parts {
		if !utf8.ValidString(part) {
			t.Fatalf("часть %d не валидный UTF-8", i)
		}
	}
	if got := strings.Join(parts, ""); got != line {
		t.Fatal("склейка частей не восстановила строку")
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

func TestSendSplitsLongMessages(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		mu.Lock()
		texts = append(texts, r.PostFormValue("text"))
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	line := strings.Repeat("x", 100)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = line
	}
	text := strings.Join(lines, "\n")

	tg := newTelegramAt(srv.URL, "123:abc", "42")
	tg.backoff = time.Millisecond
	if err := tg.Send(context.Background(), text); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 3 {
		t.Fatalf("частей %d, ожидалось 3", len(texts))
	}
	for i, part := range texts {
		if len(part) > maxMessageBytes {
			t.Fatalf("часть %d длиннее лимита: %d", i, len(part))
		}
	}
	if got := strings.Join(texts, "\n"); got != text {
		t.Fatalf("склейка частей не даёт исходный текст")
	}
}

func TestParseChatIDs(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"42", []string{"42"}},
		{"42, 100 ,200", []string{"42", "100", "200"}},
		{"42,,  ,100,", []string{"42", "100"}},
		{"  ", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := parseChatIDs(c.raw)
		if len(got) != len(c.want) {
			t.Fatalf("parseChatIDs(%q) = %v, ожидалось %v", c.raw, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("parseChatIDs(%q) = %v, ожидалось %v", c.raw, got, c.want)
			}
		}
	}
}

func TestTelegramFromEnvList(t *testing.T) {
	t.Setenv(envToken, "123:abc")
	t.Setenv(envChatID, "42")
	t.Setenv(envChatIDs, "42,100")
	tg, err := TelegramFromEnv()
	if err != nil {
		t.Fatalf("TelegramFromEnv: %v", err)
	}
	if len(tg.chatIDs) != 2 || tg.chatIDs[0] != "42" || tg.chatIDs[1] != "100" {
		t.Fatalf("получатели %v, ожидались [42 100]", tg.chatIDs)
	}
	t.Setenv(envChatIDs, "")
	tg, err = TelegramFromEnv()
	if err != nil {
		t.Fatalf("TelegramFromEnv со старой переменной: %v", err)
	}
	if len(tg.chatIDs) != 1 || tg.chatIDs[0] != "42" {
		t.Fatalf("получатели %v, ожидался [42]", tg.chatIDs)
	}
}

func TestSendContinuesAfterRecipientFailure(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		mu.Lock()
		seen = append(seen, r.PostFormValue("chat_id"))
		mu.Unlock()
		if r.PostFormValue("chat_id") == "100" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := newTelegramAt(srv.URL, "123:abc", "100", "200")
	tg.backoff = time.Millisecond
	if err := tg.Send(context.Background(), "текст"); err != nil {
		t.Fatalf("сбой одного адресата не должен ронять Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	attempts100, attempts200 := 0, 0
	for _, id := range seen {
		if id == "100" {
			attempts100++
		} else {
			attempts200++
		}
	}
	if attempts100 != tg.retries {
		t.Fatalf("сбойному получателю %d запросов, ожидалось %d попыток", attempts100, tg.retries)
	}
	if attempts200 != 1 {
		t.Fatalf("живому получателю %d запросов, ожидался один", attempts200)
	}
}
