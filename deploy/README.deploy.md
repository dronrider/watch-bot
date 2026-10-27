# Развёртывание

Сборка на машине разработки, раскладка на сервере алертов (немецкий VPS,
там же крон xr-cert-alert). Адрес и порт подключения живут в доступах
рабочей машины, здесь их нет.

1. Собрать бинарь под сервер и прогнать тесты до сборки:

       GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o resetwatch ./cmd/resetwatch
       GOWORK=off go test ./...

   GOWORK=off нужен, когда рабочий каталог машины держит свой go.work.

2. Первый выклад. На сервере завести пользователя и каталог:

       useradd -r -s /usr/sbin/nologin -d /opt/resetwatch resetwatch
       mkdir -p /opt/resetwatch/adapters

   Положить файлы: бинарь в /opt/resetwatch/resetwatch, адаптеры из
   adapters/ в /opt/resetwatch/adapters/, конфиг (копия
   configs/config.example.toml) в /opt/resetwatch/config.toml, юнит
   deploy/resetwatch.service в /etc/systemd/system/resetwatch.service.
   Каталог /opt/resetwatch отдать пользователю resetwatch целиком, бинарь
   сделать исполняемым.

3. Окружение. /opt/resetwatch/resetwatch.env с двумя строками:
   RESETWATCH_BOT_TOKEN и RESETWATCH_BOT_CHAT_IDS с получателями через
   запятую (вместо неё работает старая RESETWATCH_BOT_CHAT_ID). Файл держать под
   root:root с правами 600: systemd читает его до сброса прав на
   пользователя сервиса, а значения не должны быть доступны никому
   лишнему. Значения в чаты и командные строки не копируются.

4. Запуск и включение:

       systemctl daemon-reload
       systemctl enable --now resetwatch.service

   Журнал: `journalctl -u resetwatch -f`. Каталог состояния /var/lib/resetwatch
   создаёт systemd (StateDirectory в юните).

5. Дым. Разовый прогон не ждёт тика планировщика, поэтому проверка
   детерминированная. Во временном каталоге сервера собирается конфиг с
   одним источником на тестовом скрипте, затем:

       resetwatch -config /tmp/rw-smoke/config.toml -once

   Первый прогон закладывает события молча (холодный старт), после правки
   скрипта с новым событием второй прогон присылает живое уведомление в
   Telegram. Временный каталог после дыма удаляется.

6. Обновление выката: перекинуть бинарь и адаптеры, затем
   `systemctl restart resetwatch`. Состояние в SQLite не трогается,
   удаление базы равносильно повторной закладке истории без уведомлений.
