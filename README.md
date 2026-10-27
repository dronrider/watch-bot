# resetwatch

Детерминированный монитор сбросов лимитов Codex и Claude. По расписанию
опрашивает внешние источники, сравнивает события с состоянием и присылает
новые в Telegram. Один бинарь, расписание и состояние держит сам,
адаптеры источников это маленькие скрипты на JS.

## Быстрый старт

1. Собрать бинарь и прогнать тесты:

       CGO_ENABLED=0 go build -o resetwatch ./cmd/resetwatch
       go test ./...

2. Положить конфиг из примера и задать окружение бота:

       cp configs/config.example.toml configs/config.toml
       export RESETWATCH_BOT_TOKEN=... RESETWATCH_BOT_CHAT_IDS=...,...

3. Запустить:

       ./resetwatch -config configs/config.toml

Логи идут в stdout, под systemd их подхватывает journald.

## Устройство

- `cmd/resetwatch` читает TOML и гоняет расписание (robfig/cron), каждый
  источник со своим cron-выражением, по умолчанию раз в 15 минут.
- `internal/harness` это песочница: скрипт адаптера исполняется в goja и
  видит только `http.get`, `rss.read`, `state.get`, `state.set` и `log`.
  Скрипт возвращает события `collect()`; расписание, ретраи, дедуп и
  отправка остаются на стороне хоста.
- `internal/state` хранит всё в SQLite: дедуп `seen`, ключи адаптеров `kv`,
  свежесть источников `fresh`.
- `internal/notify` отправляет сообщения через Telegram Bot API, токен и
  chat id приходят из окружения.
- `internal/pipeline` связывает прогон адаптера с дедупом и отправкой,
  `internal/watchdog` сообщает, если источник давно молчит.
- `adapters` это скрипты источников, `internal/harness/testdata/golden`
  слепки боевой выдачи для golden-тестов.

Секреты не пишутся в конфиг: бот читает `RESETWATCH_BOT_TOKEN` и
`RESETWATCH_BOT_CHAT_IDS` из окружения, в последней через запятую
перечислены получатели уведомлений. Старая `RESETWATCH_BOT_CHAT_ID`
с единственным чатом продолжает работать как запасная. Развёртывание
на сервере описано в `deploy/README.deploy.md`.
