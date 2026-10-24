// Состояние монитора: дедуп событий, ключи адаптеров и свежесть источников.
// Даты лежат текстом RFC3339 в UTC, чтобы база читалась глазами.
package state

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS seen (
    id         TEXT NOT NULL,
    source     TEXT NOT NULL,
    first_seen TEXT NOT NULL,
    last_seen  TEXT NOT NULL,
    PRIMARY KEY (id, source)
);
CREATE TABLE IF NOT EXISTS kv (
    source TEXT NOT NULL,
    key    TEXT NOT NULL,
    value  TEXT NOT NULL,
    PRIMARY KEY (source, key)
);
CREATE TABLE IF NOT EXISTS fresh (
    source         TEXT PRIMARY KEY,
    last_ok_at     TEXT,
    last_events_at TEXT
);
`

func Open(path string) (*Store, error) {
	// Одно соединение: писатель в этой схеме всегда один, а лишние коннекты
	// к in-memory базе рвут её на отдельные копии.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("открытие %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("создание схемы: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// IsKnown говорит, встречалось ли событие с этим id у источника раньше.
func (s *Store) IsKnown(source, id string) (bool, error) {
	var one int
	err := s.db.QueryRow("SELECT 1 FROM seen WHERE source = ? AND id = ?", source, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("проверка seen: %w", err)
	}
	return true, nil
}

// MarkSeen вставляет событие или сдвигает last_seen у уже известного.
func (s *Store) MarkSeen(source, id string, at time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO seen (id, source, first_seen, last_seen) VALUES (?, ?, ?, ?)
ON CONFLICT (id, source) DO UPDATE SET last_seen = excluded.last_seen`,
		id, source, at.UTC().Format(time.RFC3339), at.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("запись seen: %w", err)
	}
	return nil
}

// Get читает ключ адаптера из пространства имён источника.
func (s *Store) Get(source, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM kv WHERE source = ? AND key = ?", source, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("чтение kv: %w", err)
	}
	return value, true, nil
}

// Set пишет ключ адаптера в пространство имён источника.
func (s *Store) Set(source, key, value string) error {
	_, err := s.db.Exec(`
INSERT INTO kv (source, key, value) VALUES (?, ?, ?)
ON CONFLICT (source, key) DO UPDATE SET value = excluded.value`,
		source, key, value)
	if err != nil {
		return fmt.Errorf("запись kv: %w", err)
	}
	return nil
}

// TouchOK отмечает успешный прогон источника.
func (s *Store) TouchOK(source string, at time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO fresh (source, last_ok_at, last_events_at) VALUES (?, ?, NULL)
ON CONFLICT (source) DO UPDATE SET last_ok_at = excluded.last_ok_at`,
		source, at.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("запись fresh: %w", err)
	}
	return nil
}

// TouchEvents отмечает прогон, на котором источник вернул события.
func (s *Store) TouchEvents(source string, at time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO fresh (source, last_ok_at, last_events_at) VALUES (?, ?, ?)
ON CONFLICT (source) DO UPDATE SET last_events_at = excluded.last_events_at`,
		source, at.UTC().Format(time.RFC3339), at.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("запись fresh: %w", err)
	}
	return nil
}

// Fresh возвращает моменты последнего успешного прогона и последнего прогона
// с событиями. Нулевые значения означают «не случалось ни разу».
func (s *Store) Fresh(source string) (lastOK, lastEvents time.Time, err error) {
	var okText, eventsText sql.NullString
	err = s.db.QueryRow("SELECT last_ok_at, last_events_at FROM fresh WHERE source = ?", source).
		Scan(&okText, &eventsText)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("чтение fresh: %w", err)
	}
	if okText.Valid {
		lastOK, err = time.Parse(time.RFC3339, okText.String)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("fresh.last_ok_at у %s: %w", source, err)
		}
	}
	if eventsText.Valid {
		lastEvents, err = time.Parse(time.RFC3339, eventsText.String)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("fresh.last_events_at у %s: %w", source, err)
		}
	}
	return lastOK, lastEvents, nil
}

// Namespaced ограничивает доступ адаптера к kv его собственным источником.
type Namespaced struct {
	Store  *Store
	Source string
}

func (n Namespaced) Get(key string) (string, bool, error) {
	return n.Store.Get(n.Source, key)
}

func (n Namespaced) Set(key, value string) error {
	return n.Store.Set(n.Source, key, value)
}
