# Развёртывание

Сборка и раскладка на сервере. Полные шаги под конкретную машину добавит
эксплуатация, адресов здесь нет.

1. Собрать бинарь на машине разработки и проверить его на слепках:

       CGO_ENABLED=0 go build -o resetwatch ./cmd/resetwatch
       go test ./...

2. На сервере создать каталоги и пользователя:

       install -d -o resetwatch -g resetwatch /opt/resetwatch /var/lib/resetwatch

3. Положить файлы: бинарь в /opt/resetwatch/resetwatch, конфиг в
   /opt/resetwatch/config.toml (копия configs/config.example.toml с
   местным state.path), окружение в /opt/resetwatch/resetwatch.env
   (копия deploy/resetwatch.env.example с токенами), юнит
   deploy/resetwatch.service в /etc/systemd/system/.

4. Запустить и включить:

       systemctl daemon-reload
       systemctl enable --now resetwatch.service

Логи смотрятся в journald: `journalctl -u resetwatch -f`. Состояние лежит
в SQLite-файле из state.path, для чистого старта его достаточно удалить.
