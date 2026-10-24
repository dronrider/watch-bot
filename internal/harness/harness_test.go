package harness

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeHTTP отвечает заготовками без сети, ошибки проверяются значением url.
type fakeHTTP struct {
	pages map[string]Response
	calls []string
}

func (f *fakeHTTP) Get(_ context.Context, url string, _ GetOptions) (Response, error) {
	f.calls = append(f.calls, url)
	page, ok := f.pages[url]
	if !ok {
		return Response{}, fmt.Errorf("нет заготовки для %s", url)
	}
	return page, nil
}

type fakeState struct {
	values map[string]string
}

func newFakeState() *fakeState {
	return &fakeState{values: map[string]string{}}
}

func (f *fakeState) Get(key string) (string, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeState) Set(key, value string) error {
	f.values[key] = value
	return nil
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunCollectAndGlobals(t *testing.T) {
	script := t.TempDir() + "/probe.js"
	body := `{
		"items": [
			{"id": "a", "title": "Сброс лимитов", "date_published": "2026-10-20T00:00:00Z"}
		]
	}`
	pages := map[string]Response{
		"https://example.test/feed": {Status: 200, ContentType: "application/json", Body: body},
	}
	src := `function collect() {
		var resp = http.get("https://example.test/feed");
		var parsed = JSON.parse(resp.body);
		if (state.get("seen_count") !== null) {
			throw new Error("ключ уже был");
		}
		state.set("seen_count", String(parsed.items.length));
		log("прочитано", parsed.items.length);
		var out = [];
		for (var i = 0; i < parsed.items.length; i++) {
			out.push({
				id: "probe:" + parsed.items[i].id,
				title: parsed.items[i].title,
				time: parsed.items[i].date_published,
				kind: "reset",
				url: "",
				note: ""
			});
		}
		return out;
	}
	`
	if err := writeFile(script, src); err != nil {
		t.Fatal(err)
	}
	deps := Deps{HTTP: &fakeHTTP{pages: pages}, State: newFakeState(), Log: quietLog()}
	events, err := Run(context.Background(), script, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(events) != 1 || events[0].ID != "probe:a" || events[0].Kind != "reset" {
		t.Fatalf("неожиданные события: %+v", events)
	}
}

func TestRunValidatesContract(t *testing.T) {
	script := t.TempDir() + "/bad.js"
	src := `function collect() {
		return [{id: "x", title: "t", time: "не дата", kind: "hint"}];
	}`
	if err := writeFile(script, src); err != nil {
		t.Fatal(err)
	}
	deps := Deps{State: newFakeState(), Log: quietLog()}
	if _, err := Run(context.Background(), script, deps); err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("ожидалась ошибка времени, получено %v", err)
	}

	src = `function collect() {
		return [{id: "x", title: "t", time: "2026-10-20T00:00:00Z", kind: "чужой"}];
	}`
	if err := writeFile(script, src); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), script, deps); err == nil || !strings.Contains(err.Error(), "словаря") {
		t.Fatalf("ожидалась ошибка kind, получено %v", err)
	}
}

func TestRunRequiresCollect(t *testing.T) {
	script := t.TempDir() + "/empty.js"
	if err := writeFile(script, "var x = 1;"); err != nil {
		t.Fatal(err)
	}
	deps := Deps{State: newFakeState(), Log: quietLog()}
	if _, err := Run(context.Background(), script, deps); err == nil || !strings.Contains(err.Error(), "collect") {
		t.Fatalf("ожидалась ошибка collect, получено %v", err)
	}
}

func TestNormalizeTime(t *testing.T) {
	got := normalizeTime("Mon, 19 Oct 2026 09:00:00 +0000")
	if got != "2026-10-19T09:00:00Z" {
		t.Fatalf("normalizeTime: %s", got)
	}
	if got := normalizeTime(""); got != "" {
		t.Fatalf("пустая дата: %s", got)
	}
	if got := normalizeTime("завтра"); got != "завтра" {
		t.Fatalf("чужой формат должен вернуться как есть: %s", got)
	}
}

func TestRealRSSParsesRSSAndAtom(t *testing.T) {
	rssBody := `<?xml version="1.0"?><rss><channel>
	<item><title>один</title><link>https://e.test/1</link>
	<pubDate>Mon, 19 Oct 2026 09:00:00 +0000</pubDate><description>текст</description></item>
	</channel></rss>`
	atomBody := `<?xml version="1.0"?><feed>
	<entry><title>два</title><link href="https://e.test/2"/>
	<published>2026-10-20T10:00:00Z</published><summary>аннотация</summary></entry>
	</feed>`
	pages := map[string]Response{
		"https://e.test/rss":  {Status: 200, ContentType: "application/rss+xml", Body: rssBody},
		"https://e.test/atom": {Status: 200, ContentType: "application/atom+xml", Body: atomBody},
	}
	client := NewRealRSS(&fakeHTTP{pages: pages})
	items, err := client.Read(context.Background(), "https://e.test/rss")
	if err != nil {
		t.Fatalf("rss: %v", err)
	}
	if len(items) != 1 || items[0].Published != "2026-10-19T09:00:00Z" {
		t.Fatalf("rss items: %+v", items)
	}
	items, err = client.Read(context.Background(), "https://e.test/atom")
	if err != nil {
		t.Fatalf("atom: %v", err)
	}
	if len(items) != 1 || items[0].Link != "https://e.test/2" || items[0].Summary != "аннотация" {
		t.Fatalf("atom items: %+v", items)
	}
}

func TestRealHTTPRetries(t *testing.T) {
	var server fakeFlaky
	srv := newFlakyServer(&server)
	defer srv.Close()
	client := &RealHTTP{Attempts: 3, Backoff: time.Millisecond, Client: srv.Client()}
	resp, err := client.Get(context.Background(), srv.URL, GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.Status != 200 || server.hits != 3 {
		t.Fatalf("ожидался успех с третьей попытки, hits=%d resp=%+v", server.hits, resp)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
