package state

import (
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSeenDedup(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if known, err := s.IsKnown("src", "e1"); err != nil || known {
		t.Fatalf("первая проверка: known=%v err=%v", known, err)
	}
	if err := s.MarkSeen("src", "e1", at); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if known, err := s.IsKnown("src", "e1"); err != nil || !known {
		t.Fatalf("вторая проверка: known=%v err=%v", known, err)
	}
	later := at.Add(2 * time.Hour)
	if err := s.MarkSeen("src", "e1", later); err != nil {
		t.Fatalf("повторный MarkSeen: %v", err)
	}
	var first, last string
	if err := s.db.QueryRow("SELECT first_seen, last_seen FROM seen WHERE source=? AND id=?", "src", "e1").
		Scan(&first, &last); err != nil {
		t.Fatal(err)
	}
	if first != "2026-10-20T00:00:00Z" || last != "2026-10-20T02:00:00Z" {
		t.Fatalf("first=%s last=%s", first, last)
	}
}

func TestKVNamespaced(t *testing.T) {
	s := open(t)
	if err := s.Set("a", "k", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b", "k", "2"); err != nil {
		t.Fatal(err)
	}
	v, found, err := s.Get("a", "k")
	if err != nil || !found || v != "1" {
		t.Fatalf("a: v=%s found=%v err=%v", v, found, err)
	}
	v, found, err = s.Get("b", "k")
	if err != nil || !found || v != "2" {
		t.Fatalf("b: v=%s found=%v err=%v", v, found, err)
	}
	_, found, err = s.Get("a", "чужой")
	if err != nil || found {
		t.Fatalf("чужой ключ: found=%v err=%v", found, err)
	}
	ns := Namespaced{Store: s, Source: "a"}
	if err := ns.Set("k", "3"); err != nil {
		t.Fatal(err)
	}
	v, _, _ = ns.Get("k")
	if v != "3" {
		t.Fatalf("namespaced set: %s", v)
	}
}

func TestFresh(t *testing.T) {
	s := open(t)
	ok, events, err := s.Fresh("src")
	if err != nil || !ok.IsZero() || !events.IsZero() {
		t.Fatalf("пустая свежесть: ok=%v events=%v err=%v", ok, events, err)
	}
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if err := s.TouchOK("src", at); err != nil {
		t.Fatal(err)
	}
	ok, events, err = s.Fresh("src")
	if err != nil || !ok.Equal(at) || !events.IsZero() {
		t.Fatalf("после TouchOK: ok=%v events=%v err=%v", ok, events, err)
	}
	if err := s.TouchEvents("src", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ok, events, err = s.Fresh("src")
	if err != nil || !ok.Equal(at) || !events.Equal(at.Add(time.Hour)) {
		t.Fatalf("после TouchEvents: ok=%v events=%v err=%v", ok, events, err)
	}
}
