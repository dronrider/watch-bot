package harness

import (
	"context"
	"strconv"
	"testing"
)

// Ссылки-обманки фикстуры: подсказка Signal из календаря и лишняя ссылка
// на статус до карточки, обе с боевой выдачи 2026-10-28.
const (
	pSignal = "https://x.com/thsottiaux/status/2115273253711409208"
	pStray  = "https://x.com/thsottiaux/status/2113162413008884099"
)

// orgPage повторяет якоря боевой выдачи codexreset.org без мегабайта
// обвязки: секция статуса с временем и лишней ссылкой на статус до
// календаря, панели прогноза и карточка подтверждения поста после
// карточки-подсказки, как в живой выдаче 2026-10-28.
func orgPage(resetAt, post string, p24, p48 int) string {
	confirmedCard := ""
	if post != "" {
		confirmedCard = `<article class="space-y-3" data-testid="calendar-event">` +
			`<p class="text-xs uppercase">Confirmed, 06:49 UTC</p>` +
			`<a href="` + post + `" target="_blank">View original post on X</a>` +
			`</article>`
	}
	v24 := strconv.Itoa(p24)
	v48 := strconv.Itoa(p48)
	return `<html><body><section data-testid="current-reset-status">` +
		`<h2>Quota restored</h2>` +
		`<p data-testid="reset-elapsed-time">9 hours ago</p>` +
		`<time class="mt-4 block" dateTime="` + resetAt + `" data-testid="reset-exact-time">Oct 27, 2026</time>` +
		`<p>Stay tuned: <a href="` + pStray + `">reset status</a></p>` +
		`</section><section data-testid="forecast-panel">` +
		`<div role="img" data-testid="probability-ring-24h" data-animation="count-up-ring" data-animated-value="` + v24 + `" data-target-value="` + v24 + `" data-unit="percent"><span>` + v24 + `%</span></div>` +
		`<div role="img" data-testid="probability-ring-48h" data-animation="count-up-ring" data-animated-value="` + v48 + `" data-target-value="` + v48 + `" data-unit="percent"><span>` + v48 + `%</span></div>` +
		`</section><section data-testid="reset-calendar">` +
		`<article class="space-y-3" data-testid="calendar-event">` +
		`<p class="text-xs uppercase">Signal, 02:43 UTC</p>` +
		`<a href="` + pSignal + `" target="_blank">View original post on X</a>` +
		`</article>` +
		confirmedCard +
		`</section></body></html>`
}

// TestCodexresetorgAnchors гоняет адаптер по цепочке страниц с одним
// состоянием: холодный старт закладывает якоря молча, события дают только
// смена времени сброса и смена категории прогноза вместе с возвратом,
// колебания процента внутри категории, лишние ссылки на статус вне
// карточки подтверждения и пропавшая вёрстка молчат и якорей не трогают.
func TestCodexresetorgAnchors(t *testing.T) {
	const (
		orgURL = "https://codexreset.org/"
		t1     = "2026-10-25T06:06:16.000Z"
		t2     = "2026-10-27T10:50:27.421Z"
		t3     = "2026-10-29T06:03:00.000Z"
		p1     = "https://x.com/thsottiaux/status/2114237083652882932"
		p2     = "https://x.com/thsottiaux/status/2114588174249071102"
	)
	pages := map[string]Response{orgURL: {Status: 200, ContentType: "text/html", Body: orgPage(t1, p1, 35, 30)}}
	state := newFakeState()
	deps := Deps{HTTP: &fakeHTTP{pages: pages}, State: state, Log: quietLog()}
	run := func(t *testing.T) []Event {
		t.Helper()
		events, err := Run(context.Background(), "../../adapters/codexresetorg.js", deps)
		if err != nil {
			t.Fatalf("прогон адаптера: %v", err)
		}
		return events
	}

	if got := run(t); len(got) != 0 {
		t.Fatalf("холодный старт принёс события: %+v", got)
	}
	anchors := map[string]string{"last_reset": t1, "last_post": p1, "f24": "low", "f48": "low"}
	for key, want := range anchors {
		if got, _, _ := state.Get(key); got != want {
			t.Fatalf("после холодного старта %s = %q, хочу %q", key, got, want)
		}
	}

	if got := run(t); len(got) != 0 {
		t.Fatalf("повтор той же страницы принёс события: %+v", got)
	}

	// Новый сброс с новым постом и смена категории 24 ч дают два события,
	// панель 48 ч в прежней категории молчит. Пост идёт в url события
	// сброса, чтобы сквозной дедуп склеил его с пересказом того же поста.
	pages[orgURL] = Response{Status: 200, ContentType: "text/html", Body: orgPage(t2, p2, 45, 30)}
	got := run(t)
	if len(got) != 2 {
		t.Fatalf("ожидал 2 события, получено %+v", got)
	}
	if got[0].ID != "codexresetorg:reset:"+t2 || got[0].Kind != "reset" || got[0].URL != p2 {
		t.Fatalf("событие сброса: %+v", got[0])
	}
	if got[1].ID != "codexresetorg:forecast-24h:low>medium:1" || got[1].Kind != "forecast" || got[1].Time != t2 {
		t.Fatalf("событие прогноза: %+v", got[1])
	}

	pages[orgURL] = Response{Status: 200, ContentType: "text/html", Body: orgPage(t2, p2, 48, 30)}
	if got := run(t); len(got) != 0 {
		t.Fatalf("колебание процента внутри категории принесло события: %+v", got)
	}

	// Сброс без нового поста это раннее восстановление по наблюдению
	// монитора: ссылка ведёт на сам сайт, чужой пост не приписывается.
	pages[orgURL] = Response{Status: 200, ContentType: "text/html", Body: orgPage(t3, p2, 48, 30)}
	got = run(t)
	if len(got) != 1 || got[0].Kind != "reset" || got[0].URL != orgURL {
		t.Fatalf("сброс без нового поста: %+v", got)
	}

	// Возврат категории 48 ч: уход low -> medium и обратный medium -> low
	// дают события с переходом и номером смены в id, поэтому вечный
	// seen-дедуп хоста не глушит повторный проход того же цикла.
	pages[orgURL] = Response{Status: 200, ContentType: "text/html", Body: orgPage(t3, p2, 48, 55)}
	got = run(t)
	if len(got) != 1 || got[0].ID != "codexresetorg:forecast-48h:low>medium:1" ||
		got[0].Kind != "forecast" || got[0].Time != t3 {
		t.Fatalf("уход 48 ч в medium: %+v", got)
	}
	pages[orgURL] = Response{Status: 200, ContentType: "text/html", Body: orgPage(t3, p2, 48, 30)}
	got = run(t)
	if len(got) != 1 || got[0].ID != "codexresetorg:forecast-48h:medium>low:2" ||
		got[0].Kind != "forecast" || got[0].Time != t3 {
		t.Fatalf("возврат 48 ч в low: %+v", got)
	}
	if v, _, _ := state.Get("f48"); v != "low" {
		t.Fatalf("после возврата f48 = %q, хочу low", v)
	}

	pages[orgURL] = Response{Status: 200, ContentType: "text/html", Body: "<html><body>техобслуживание</body></html>"}
	if got := run(t); len(got) != 0 {
		t.Fatalf("страница без якорей принесла события: %+v", got)
	}
	if v, _, _ := state.Get("last_reset"); v != t3 {
		t.Fatalf("страница без якорей тронула якорь: last_reset = %q", v)
	}

	pages[orgURL] = Response{Status: 503, ContentType: "text/html", Body: "unavailable"}
	if got := run(t); len(got) != 0 {
		t.Fatalf("ответ 503 принёс события: %+v", got)
	}
	if v, _, _ := state.Get("last_reset"); v != t3 {
		t.Fatalf("ответ 503 тронул якорь: last_reset = %q", v)
	}
}
