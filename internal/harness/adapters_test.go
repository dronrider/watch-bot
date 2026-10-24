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
			deps := Deps{
				HTTP:  httpFake,
				RSS:   rss,
				State: newFakeState(),
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
