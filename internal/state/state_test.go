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

func TestSubscribers(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	if ids, err := s.SubscriberIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("пустой список: ids=%v err=%v", ids, err)
	}
	if added, err := s.Subscribe("100", at); err != nil || !added {
		t.Fatalf("первая подписка: added=%v err=%v", added, err)
	}
	if added, err := s.Subscribe("100", at.Add(time.Minute)); err != nil || added {
		t.Fatalf("повторная подписка: added=%v err=%v", added, err)
	}
	if added, err := s.Subscribe("200", at.Add(time.Minute)); err != nil || !added {
		t.Fatalf("вторая подписка: added=%v err=%v", added, err)
	}
	ids, err := s.SubscriberIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "100" || ids[1] != "200" {
		t.Fatalf("подписчики %v, ожидались [100 200]", ids)
	}
	removed, err := s.Unsubscribe("100")
	if err != nil || !removed {
		t.Fatalf("отписка: removed=%v err=%v", removed, err)
	}
	removed, err = s.Unsubscribe("100")
	if err != nil || removed {
		t.Fatalf("повторная отписка: removed=%v err=%v", removed, err)
	}
	ids, err = s.SubscriberIDs()
	if err != nil || len(ids) != 1 || ids[0] != "200" {
		t.Fatalf("подписчики после отписки %v, ожидался [200]", ids)
	}
}

func TestSubscribersSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	if added, err := s.Subscribe("-100", at); err != nil || !added {
		t.Fatalf("подписка: added=%v err=%v", added, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("повторное Open: %v", err)
	}
	defer reopened.Close()
	ids, err := reopened.SubscriberIDs()
	if err != nil || len(ids) != 1 || ids[0] != "-100" {
		t.Fatalf("подписчики после переоткрытия %v, ожидался [-100]", ids)
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
