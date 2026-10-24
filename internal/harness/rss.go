package harness

import (
	"context"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// Item это один пост ленты, который rss.read отдаёт скрипту.
type Item struct {
	Title     string `json:"title"`
	Link      string `json:"link"`
	Published string `json:"published"`
	Summary   string `json:"summary"`
}

// RSSClient читает ленту источника. Парсинг всегда на стороне хоста,
// скрипт получает готовые поля.
type RSSClient interface {
	Read(ctx context.Context, url string) ([]Item, error)
}

type RealRSS struct {
	HTTP HTTPClient
}

func NewRealRSS(httpClient HTTPClient) *RealRSS {
	return &RealRSS{HTTP: httpClient}
}

// xmlFeed покрывает и RSS (channel/item), и Atom (feed/entry) одной
// структурой: Unmarshal сам берёт совпавшие поля.
type xmlFeed struct {
	Channel struct {
		Items []xmlItem `xml:"item"`
	} `xml:"channel"`
	Entries []xmlEntry `xml:"entry"`
}

type xmlItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

type xmlEntry struct {
	Title   string  `xml:"title"`
	Link    xmlLink `xml:"link"`
	Pub     string  `xml:"published"`
	Updated string  `xml:"updated"`
	Summary string  `xml:"summary"`
	Content string  `xml:"content"`
}

type xmlLink struct {
	Href string `xml:"href,attr"`
}

var timeLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339,
	"2006-01-02",
}

func (r *RealRSS) Read(ctx context.Context, url string) ([]Item, error) {
	resp, err := r.HTTP.Get(ctx, url, GetOptions{Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("лента %s: ответ %d", url, resp.Status)
	}
	var feed xmlFeed
	if err := xml.Unmarshal([]byte(resp.Body), &feed); err != nil {
		return nil, fmt.Errorf("разбор ленты %s: %w", url, err)
	}
	items := make([]Item, 0, len(feed.Channel.Items)+len(feed.Entries))
	for _, it := range feed.Channel.Items {
		items = append(items, Item{
			Title:     it.Title,
			Link:      it.Link,
			Published: normalizeTime(it.PubDate),
			Summary:   it.Description,
		})
	}
	for _, e := range feed.Entries {
		summary := e.Summary
		if summary == "" {
			summary = e.Content
		}
		published := e.Pub
		if published == "" {
			published = e.Updated
		}
		items = append(items, Item{
			Title:     e.Title,
			Link:      e.Link.Href,
			Published: normalizeTime(published),
			Summary:   summary,
		})
	}
	return items, nil
}

// normalizeTime приводит дату ленты к RFC3339, формат снаружи неизвестен.
func normalizeTime(s string) string {
	if s == "" {
		return ""
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}

func jsRSSRead(vm *goja.Runtime, ctx context.Context, deps Deps, call goja.FunctionCall) goja.Value {
	url := call.Argument(0).String()
	if url == "" {
		panic(vm.NewGoError(fmt.Errorf("rss.read: пустой url")))
	}
	items, err := deps.RSS.Read(ctx, url)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]interface{}{
			"title":     it.Title,
			"link":      it.Link,
			"published": it.Published,
			"summary":   it.Summary,
		})
	}
	return vm.ToValue(out)
}
