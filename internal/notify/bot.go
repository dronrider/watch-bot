// Бот подписок: длинный опрос getUpdates принимает команды /start и /stop,
// подписки дополняют список получателей из env.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// pollTimeout это длинный опрос: сервер Telegram придерживает ответ, пока
// не появятся обновления или не выйдет срок.
const pollTimeout = 50 * time.Second

// botKVSource и botKVKey хранят курсор обновлений в kv: без записи рестарт
// проиграл бы команды последних суток заново.
const (
	botKVSource = "bot"
	botKVKey    = "update_offset"
)

// BotStore хранит подписки и курсор обновлений бота: таблица subscribers
// и kv из state.
type BotStore interface {
	Subscriptions
	Get(source, key string) (string, bool, error)
	Set(source, key, value string) error
}

type Bot struct {
	api
	Store BotStore
	// pause это отдых после неудачного опроса.
	pause time.Duration
	Log   *slog.Logger
}

// Bot собирает бота на том же токене и клиенте, что и рассылка уведомлений.
func (t *Telegram) Bot(store BotStore) *Bot {
	return &Bot{api: t.api, Store: store, pause: 5 * time.Second}
}

// newBotAt нужен тестам: base URL подменяется на локальный сервер.
func newBotAt(apiBase, token string, store BotStore) *Bot {
	return &Bot{api: newAPI(apiBase, token), Store: store, pause: 5 * time.Second}
}

// Run крутит опрос до отмены контекста. Сбой опроса не останавливает бот:
// пауза и следующая попытка.
func (b *Bot) Run(ctx context.Context) {
	offset := b.loadOffset()
	for ctx.Err() == nil {
		next, err := b.poll(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.log().Warn("опрос getUpdates не удался", "err", err)
			if !sleep(ctx, b.pause) {
				return
			}
			continue
		}
		offset = next
	}
}

// poll забирает порцию обновлений, проводит команды и возвращает курсор
// следующего опроса.
func (b *Bot) poll(ctx context.Context, offset int64) (int64, error) {
	updates, err := b.getUpdates(ctx, offset)
	if err != nil {
		return offset, err
	}
	for _, u := range updates {
		b.handle(ctx, u)
		if u.UpdateID >= offset {
			offset = u.UpdateID + 1
		}
	}
	if len(updates) > 0 {
		b.saveOffset(offset)
	}
	return offset, nil
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	Chat tgChat `json:"chat"`
	Text string `json:"text"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]tgUpdate, error) {
	form := url.Values{}
	form.Set("offset", strconv.FormatInt(offset, 10))
	form.Set("timeout", strconv.FormatInt(int64(pollTimeout.Seconds()), 10))
	body, err := b.call(ctx, "getUpdates", form)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("getUpdates: разбор ответа: %w", err)
	}
	return resp.Result, nil
}

// handle проводит одно обновление: /start подписывает чат, /stop снимает
// подписку, обе команды подтверждаются сообщением в чат.
func (b *Bot) handle(ctx context.Context, u tgUpdate) {
	if u.Message == nil {
		return
	}
	chatID := strconv.FormatInt(u.Message.Chat.ID, 10)
	switch command(u.Message.Text) {
	case "/start":
		added, err := b.Store.Subscribe(chatID, time.Now().UTC())
		if err != nil {
			b.log().Warn("подписка не записана", "chat", chatID, "err", err)
			b.reply(ctx, chatID, "Подписка не записалась, попробуйте ещё раз.")
			return
		}
		if added {
			b.reply(ctx, chatID, "Подписка оформлена, уведомления о сбросах лимитов будут приходить в этот чат. /stop снимет подписку.")
			return
		}
		b.reply(ctx, chatID, "Подписка уже активна.")
	case "/stop":
		removed, err := b.Store.Unsubscribe(chatID)
		if err != nil {
			b.log().Warn("отписка не записана", "chat", chatID, "err", err)
			b.reply(ctx, chatID, "Отписка не записалась, попробуйте ещё раз.")
			return
		}
		if removed {
			b.reply(ctx, chatID, "Подписка снята, уведомления приходить больше не будут.")
			return
		}
		b.reply(ctx, chatID, "Подписки не было.")
	}
}

// command выделяет команду из текста сообщения: в группах к команде
// добавляется имя бота через собаку.
func command(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	cmd := fields[0]
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd
}

func (b *Bot) reply(ctx context.Context, chatID, text string) {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	if _, err := b.call(ctx, "sendMessage", form); err != nil {
		b.log().Warn("подтверждение не дошло", "chat", chatID, "err", err)
	}
}

func (b *Bot) loadOffset() int64 {
	raw, found, err := b.Store.Get(botKVSource, botKVKey)
	if err != nil {
		b.log().Warn("чтение курсора обновлений не удалось, опрос с нуля", "err", err)
		return 0
	}
	if !found {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		b.log().Warn("курсор обновлений не разобран, опрос с нуля", "raw", raw, "err", err)
		return 0
	}
	return n
}

func (b *Bot) saveOffset(offset int64) {
	if err := b.Store.Set(botKVSource, botKVKey, strconv.FormatInt(offset, 10)); err != nil {
		b.log().Warn("запись курсора обновлений не удалась", "err", err)
	}
}

func (b *Bot) log() *slog.Logger {
	if b.Log != nil {
		return b.Log
	}
	return slog.Default()
}

// sleep ждёт длительность и говорит, дождался ли конца или контекст отменён.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
