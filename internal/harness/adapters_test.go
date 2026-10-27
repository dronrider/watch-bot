package harness

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "перезаписать golden-файлы")

// Golden-тест адаптеров: настоящий скрипт из adapters/ прогоняется на
// реальном слепке источника из testdata/snapshots, результат
// сверяется с golden-файлом.
func TestAdapterGolden(t *testing.T) {
	cases := []struct {
		name     string
		script   string
		url      string
		snapshot string
		state    map[string]string
	}{
		{
			name:     "clauderesets",
			script:   "../../adapters/clauderesets.js",
			url:      "https://clauderesets.com/feed.json",
			snapshot: "../../testdata/snapshots/clauderesets.feed.json",
		},
		{
			name:     "modelyard",
			script:   "../../adapters/modelyard.js",
			url:      "https://tibo.modelyard.dev/feed.xml",
			snapshot: "../../testdata/snapshots/modelyard.feed.xml",
		},
		{
			name:     "codexresetdev",
			script:   "../../adapters/codexresetdev.js",
			url:      "https://codexreset.dev/api/history",
			snapshot: "../../testdata/snapshots/codexresetdev.api.json",
		},
		{
			// Слепок боевой выдачи 2026-10-27: сброс в 10:50 UTC при
			// прошлом 06:06 2026-10-25, категория 24 ч ушла в низкий,
			// 48 ч выросла до среднего.
			name:     "codexresetorg",
			script:   "../../adapters/codexresetorg.js",
			url:      "https://codexreset.org/",
			snapshot: "../../testdata/snapshots/codexresetorg.html",
			state: map[string]string{
				"last_reset": "2026-10-25T06:06:16.000Z",
				"last_post":  "https://x.com/thsottiaux/status/2114237083652882932",
				"f24":        "medium",
				"f48":        "low",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := os.ReadFile(tc.snapshot)
			if err != nil {
				t.Fatalf("слепок: %v", err)
			}
			pages := map[string]Response{
				tc.url: {Status: 200, ContentType: "text/plain", Body: string(snapshot)},
			}
			httpFake := &fakeHTTP{pages: pages}
			rss := NewRealRSS(httpFake)
			state := newFakeState()
			for k, v := range tc.state {
				state.values[k] = v
			}
			deps := Deps{
				HTTP:  httpFake,
				RSS:   rss,
				State: state,
				Log:   quietLog(),
			}
			events, err := Run(context.Background(), tc.script, deps)
			if err != nil {
				t.Fatalf("прогон адаптера: %v", err)
			}
			got, err := json.MarshalIndent(events, "", "  ")
			if err != nil {
				t.Fatalf("маршал: %v", err)
			}
			goldenPath := filepath.Join("testdata", "golden", tc.name+".json")
			if *update {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("golden (запусти с -update): %v", err)
			}
			if string(want) != string(got)+"\n" {
				t.Fatalf("расхождение с golden, получено:\n%s", got)
			}
		})
	}
}
