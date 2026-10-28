// Уведомления. Отправка не обязана проходить с первого раза: ошибка
// повторяется с бэкоффом и в крайнем случае возвращается вызывающему,
// чтобы уронить его цикл.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Notifier это канал уведомлений. Реализация одна, интерфейс держит
// конвейер и тесты развязанными с Telegram.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

const (
	envToken   = "RESETWATCH_BOT_TOKEN"
	envChatIDs = "RESETWATCH_BOT_CHAT_IDS"
	envChatID  = "RESETWATCH_BOT_CHAT_ID"
)

// TelegramFromEnv собирает клиента из переменных окружения: токен и chat id
// не ездят в конфиге и в базе. Список получателей берётся из RESETWATCH_BOT_CHAT_IDS,
// старая переменная с единственным чатом продолжает работать.
func TelegramFromEnv() (*Telegram, error) {
	token := os.Getenv(envToken)
	if token == "" {
		return nil, fmt.Errorf("нет %s", envToken)
	}
	raw := os.Getenv(envChatIDs)
	if raw == "" {
		raw = os.Getenv(envChatID)
	}
	chatIDs := parseChatIDs(raw)
	if len(chatIDs) == 0 {
		return nil, fmt.Errorf("нет %s и %s", envChatIDs, envChatID)
	}
	return NewTelegram(token, chatIDs...), nil
}

// parseChatIDs разбирает список получателей через запятую, пустые элементы
// выкидываются.
func parseChatIDs(raw string) []string {
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

type Telegram struct {
	apiBase string
	token   string
	chatIDs []string
	client  *http.Client
	retries int
	backoff time.Duration
	Log     *slog.Logger
}

func NewTelegram(token string, chatIDs ...string) *Telegram {
	return &Telegram{
		apiBase: "https://api.telegram.org",
		token:   token,
		chatIDs: chatIDs,
		client:  &http.Client{},
		retries: 3,
		backoff: 2 * time.Second,
	}
}

// newTelegramAt нужен тестам: base URL подменяется на локальный сервер.
func newTelegramAt(apiBase, token string, chatIDs ...string) *Telegram {
	t := NewTelegram(token, chatIDs...)
	t.apiBase = apiBase
	return t
}

// maxMessageBytes режет сообщения с запасом под лимит Telegram в 4096
// «символов» UTF-16 и служебные поля запроса.
const maxMessageBytes = 3500

// Send обходит всех получателей: неудача одному адресату пишется в журнал
// и не отменяет остальных. Ошибку наверх возвращает только полный провал,
// когда сообщения не дошли никому.
func (t *Telegram) Send(ctx context.Context, text string) error {
	parts := splitMessage(text)
	failed := 0
	for _, chatID := range t.chatIDs {
		for _, part := range parts {
			if err := t.sendPart(ctx, chatID, part); err != nil {
				t.log().Warn("уведомление не дошло", "chat", chatID, "err", err)
				failed++
				break
			}
		}
	}
	if failed == len(t.chatIDs) {
		return fmt.Errorf("sendMessage: не дошло ни одному из %d получателей", len(t.chatIDs))
	}
	return nil
}

func (t *Telegram) log() *slog.Logger {
	if t.Log != nil {
		return t.Log
	}
	return slog.Default()
}

// splitMessage делит текст по границам строк, а строку длиннее лимита
// режет по байтам: события в сообщении завершены построчно, поэтому части
// остаются валидным HTML.
func splitMessage(text string) []string {
	if len(text) <= maxMessageBytes {
		return []string{text}
	}
	var parts []string
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		chunk := line
		if b.Len() > 0 {
			chunk = "\n" + line
		}
		if b.Len()+len(chunk) > maxMessageBytes && b.Len() > 0 {
			parts = append(parts, b.String())
			b.Reset()
			chunk = line
		}
		for len(chunk) > maxMessageBytes {
			cut := maxMessageBytes
			// Рез по границе руна: разрез внутри эмодзи оставил бы Telegram
			// битый UTF-8.
			for cut > 0 && !utf8.RuneStart(chunk[cut]) {
				cut--
			}
			parts = append(parts, chunk[:cut])
			chunk = chunk[cut:]
		}
		b.WriteString(chunk)
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func (t *Telegram) sendPart(ctx context.Context, chatID, text string) error {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("parse_mode", "HTML")
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, t.token)

	var lastErr error
	for attempt := 0; attempt < t.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(t.backoff * time.Duration(attempt)):
			}
		}
		err := t.once(ctx, endpoint, form)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable(err) {
			return err
		}
	}
	return fmt.Errorf("sendMessage после %d попыток: %w", t.retries, lastErr)
}

func (t *Telegram) once(ctx context.Context, endpoint string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return &apiError{status: resp.StatusCode, body: string(body)}
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("sendMessage: ответ %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(string(body), `"ok":true`) {
		return fmt.Errorf("sendMessage: telegram отказал: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("sendMessage: ответ %d: %s", e.status, strings.TrimSpace(e.body))
}

func retryable(err error) bool {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return true
	}
	return !errors.Is(err, context.Canceled)
}
