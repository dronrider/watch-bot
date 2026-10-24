// Уведомления. Отправка не обязана проходить с первого раза: ошибка
// повторяется с бэкоффом и в крайнем случае возвращается вызывающему,
// чтобы уронить его цикл.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Notifier это канал уведомлений. Реализация одна, интерфейс держит
// конвейер и тесты развязанными с Telegram.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

const (
	envToken  = "RESETWATCH_BOT_TOKEN"
	envChatID = "RESETWATCH_BOT_CHAT_ID"
)

// TelegramFromEnv собирает клиента из переменных окружения: токен и chat id
// не ездят в конфиге и в базе.
func TelegramFromEnv() (*Telegram, error) {
	token := os.Getenv(envToken)
	if token == "" {
		return nil, fmt.Errorf("нет %s", envToken)
	}
	chatID := os.Getenv(envChatID)
	if chatID == "" {
		return nil, fmt.Errorf("нет %s", envChatID)
	}
	return NewTelegram(token, chatID), nil
}

type Telegram struct {
	apiBase string
	token   string
	chatID  string
	client  *http.Client
	retries int
	backoff time.Duration
}

func NewTelegram(token, chatID string) *Telegram {
	return &Telegram{
		apiBase: "https://api.telegram.org",
		token:   token,
		chatID:  chatID,
		client:  &http.Client{},
		retries: 3,
		backoff: 2 * time.Second,
	}
}

// newTelegramAt нужен тестам: base URL подменяется на локальный сервер.
func newTelegramAt(apiBase, token, chatID string) *Telegram {
	t := NewTelegram(token, chatID)
	t.apiBase = apiBase
	return t
}

func (t *Telegram) Send(ctx context.Context, text string) error {
	form := url.Values{}
	form.Set("chat_id", t.chatID)
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
