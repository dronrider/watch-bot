// Уведомления и бот подписки. Отправка не обязана проходить с первого раза:
// ошибка повторяется с бэкоффом и в крайнем случае возвращается вызывающему,
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
	api
	chatIDs []string
	// Subs подаёт подписчиков команды /start сверх списка из env, поле
	// необязательное.
	Subs Subscriptions
	Log  *slog.Logger
}

func NewTelegram(token string, chatIDs ...string) *Telegram {
	return &Telegram{
		api:     newAPI("https://api.telegram.org", token),
		chatIDs: chatIDs,
	}
}

// newTelegramAt нужен тестам: base URL подменяется на локальный сервер.
func newTelegramAt(apiBase, token string, chatIDs ...string) *Telegram {
	t := NewTelegram(token, chatIDs...)
	t.api.apiBase = apiBase
	return t
}

// Subscriptions хранит подписки получателей: рассылка дополняет ими список
// из env, бот записывает сюда команды /start и /stop.
type Subscriptions interface {
	Subscribe(chatID string, at time.Time) (added bool, err error)
	Unsubscribe(chatID string) (removed bool, err error)
	SubscriberIDs() ([]string, error)
}

// maxMessageBytes режет сообщения с запасом под лимит Telegram в 4096
// «символов» UTF-16 и служебные поля запроса.
const maxMessageBytes = 3500

// Send обходит всех получателей: чаты из env плюс активные подписчики,
// неудача одному адресату пишется в журнал с chat id и не отменяет остальных.
// Ошибку наверх возвращает только полный провал, когда сообщения не дошли
// никому.
func (t *Telegram) Send(ctx context.Context, text string) error {
	chatIDs := t.recipients()
	parts := splitMessage(text)
	failed := 0
	for _, chatID := range chatIDs {
		for _, part := range parts {
			if err := t.sendPart(ctx, chatID, part); err != nil {
				t.log().Warn("уведомление не дошло", "chat", chatID, "err", err)
				failed++
				break
			}
		}
	}
	if failed == len(chatIDs) {
		return fmt.Errorf("sendMessage: не дошло ни одному из %d получателей", len(chatIDs))
	}
	return nil
}

// recipients объединяет чаты из env с активными подписчиками: подписка через
// /start работает без правки окружения, повторяющиеся адреса сходятся в один.
// Сбой чтения подписок не глушит рассылку по списку из env.
func (t *Telegram) recipients() []string {
	ids := make([]string, len(t.chatIDs))
	copy(ids, t.chatIDs)
	if t.Subs == nil {
		return ids
	}
	subs, err := t.Subs.SubscriberIDs()
	if err != nil {
		t.log().Warn("чтение подписок не удалось", "err", err)
		return ids
	}
	seen := make(map[string]bool, len(ids)+len(subs))
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range subs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
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
	_, err := t.call(ctx, "sendMessage", form)
	return err
}

// api это клиент Telegram Bot API: адрес, токен и повторы общие у рассылки
// уведомлений и бота подписок.
type api struct {
	apiBase string
	token   string
	client  *http.Client
	retries int
	backoff time.Duration
}

func newAPI(apiBase, token string) api {
	return api{
		apiBase: apiBase,
		token:   token,
		client:  &http.Client{},
		retries: 3,
		backoff: 2 * time.Second,
	}
}

// call дергает метод API с повторами и бэкоффом: сетевой сбой и ответ 5xx
// повторяются, отказ бота и отмена контекста нет.
func (a api) call(ctx context.Context, method string, form url.Values) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/bot%s/%s", a.apiBase, a.token, method)

	var lastErr error
	for attempt := 0; attempt < a.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(a.backoff * time.Duration(attempt)):
			}
		}
		body, err := a.once(ctx, method, endpoint, form)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%s после %d попыток: %w", method, a.retries, lastErr)
}

func (a api) once(ctx context.Context, method, endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &apiError{method: method, status: resp.StatusCode, body: string(body)}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: ответ %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(string(body), `"ok":true`) {
		return nil, fmt.Errorf("%s: telegram отказал: %s", method, strings.TrimSpace(string(body)))
	}
	return body, nil
}

type apiError struct {
	method string
	status int
	body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s: ответ %d: %s", e.method, e.status, strings.TrimSpace(e.body))
}

func retryable(err error) bool {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return true
	}
	return !errors.Is(err, context.Canceled)
}
