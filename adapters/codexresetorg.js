// Источник: https://codexreset.org/. Формат сверен с боевой выдачей 2026-10-27:
// одностраничник без API и фида, время последнего сброса стоит в теге time с
// data-testid="reset-exact-time" и атрибутом dateTime, оценки сброса на 24 и
// 48 часов в панелях probability-ring-24h/-48h с процентом в data-target-value.
// Подтверждение сброса это карточка в календаре: article с
// data-testid="calendar-event" и заголовком Confirmed, ссылка на пост в X
// берётся только из неё, прочие ссылки на статусы и карточки-подсказки
// молчат. Категория это бакет процента (low ниже 40, medium до 70, дальше
// high): событием становится смена якоря или категории, колебания процента
// внутри категории молчат. Прошлые значения лежат в kv (last_reset,
// last_post, f24, f48, f24n, f48n), первый прогон закладывает якоря молча.
// Пропавшая вёрстка не трогает kv и пишет причину в лог хоста.

var pageURL = "https://codexreset.org/";
var postLink = /href="(https:\/\/x\.com\/[A-Za-z0-9_]{1,20}\/status\/\d+)"/;
var catText = {
    low: { nom: "низкий", gen: "низкого", ins: "низким" },
    medium: { nom: "средний", gen: "среднего", ins: "средним" },
    high: { nom: "высокий", gen: "высокого", ins: "высоким" }
};
var hoursWord = { "24h": "24 часа", "48h": "48 часов" };

function collect() {
    var resp = http.get(pageURL);
    if (resp.status !== 200) {
        log("codexreset.org: ответ " + resp.status + ", событий нет");
        return [];
    }
    var html = resp.body;
    var events = [];

    var resetAt = resetTime(html);
    if (resetAt === null) {
        log("codexreset.org: тег времени не найден, вёрстка могла смениться");
    }
    var post = confirmedPostLink(html);
    if (post === "") {
        log("codexreset.org: пост в X на странице не найден");
    }

    if (resetAt !== null) {
        collectReset(events, resetAt, post);
        collectForecast(events, resetAt, html, "24h", "f24");
        collectForecast(events, resetAt, html, "48h", "f48");
    }
    return events;
}

// Событие смены времени последнего сброса. Пост из X прикладывается, когда
// вместе со временем пришёл и новый пост: сброс, подтверждённый трекером,
// склеивается сквозным дедупом с пересказом того же поста другим источником.
// Без нового поста это раннее восстановление по наблюдению монитора.
function collectReset(events, resetAt, post) {
    var prev = state.get("last_reset");
    if (prev === null) {
        state.set("last_reset", resetAt);
        state.set("last_post", post);
        log("codexreset.org: первый прогон, время сброса заложено молча");
        return;
    }
    var prevPost = state.get("last_post");
    if (post !== prevPost) {
        state.set("last_post", post);
    }
    if (resetAt === prev) {
        return;
    }
    var confirmed = post !== "" && post !== prevPost;
    events.push({
        id: "codexresetorg:reset:" + resetAt,
        title: "Сброс лимитов в " + resetAt.substring(11, 16) + " UTC",
        time: resetAt,
        kind: "reset",
        url: confirmed ? post : pageURL,
        note: "Точное время последнего сброса с главной codexreset.org." +
            (confirmed ? " Подтверждено постом в X." : " Пост в X ещё не показан, восстановление по наблюдению монитора.")
    });
    state.set("last_reset", resetAt);
}

// Событие смены категории прогноза одной из панелей. Времени у панели нет,
// поэтому событие берёт отметку состояния страницы из якоря сброса. В id
// едут переход категории и номер смены из kv: seen-дедуп хоста вечный, и
// id по одной категории или по одному переходу заглушит второй проход
// того же цикла low -> medium -> low.
function collectForecast(events, resetAt, html, panel, kvKey) {
    var pct = panelPercent(html, panel);
    if (pct === null) {
        log("codexreset.org: панель прогноза " + panel + " не найдена, вёрстка могла смениться");
        return;
    }
    var cat = pct < 40 ? "low" : (pct < 70 ? "medium" : "high");
    var prev = state.get(kvKey);
    if (prev === null) {
        state.set(kvKey, cat);
        log("codexreset.org: первый прогон, категория " + panel + " заложена молча");
        return;
    }
    if (cat === prev) {
        return;
    }
    var seq = state.get(kvKey + "n");
    var n = seq === null ? 1 : parseInt(seq, 10) + 1;
    events.push({
        id: "codexresetorg:forecast-" + panel + ":" + prev + ">" + cat + ":" + n,
        title: "Прогноз на " + hoursWord[panel] + " стал " + catText[cat].ins + " (" + pct + "%)",
        time: resetAt,
        kind: "forecast",
        url: pageURL,
        note: "Категория сменилась " + (prev === "medium" ? "со " : "с ") + catText[prev].gen +
            " на " + catText[cat].nom + ". Панель на сайте помечена experimental."
    });
    state.set(kvKey, cat);
    state.set(kvKey + "n", String(n));
}

function resetTime(html) {
    var m = html.match(/<time\b[^>]*data-testid="reset-exact-time"[^>]*>/i);
    if (m === null) {
        return null;
    }
    var d = m[0].match(/dateTime="([^"]+)"/i);
    return d === null ? null : d[1];
}

// Пост в X берётся из карточки подтверждения в календаре: article с
// data-testid="calendar-event", чей заголовок начинается с Confirmed.
// Первой ссылкой на статус в документе бывает карточка-подсказка вроде
// Signal или ссылка в новом блоке до календаря, поэтому срез по всей
// странице молча подменял бы пост подтверждения чужим.
function confirmedPostLink(html) {
    var parts = html.split('data-testid="calendar-event"');
    for (var i = 1; i < parts.length; i++) {
        var card = parts[i];
        var end = card.indexOf("</article>");
        if (end !== -1) {
            card = card.substring(0, end);
        }
        if (!/^>[^<]*<p[^>]*>Confirmed/.test(card)) {
            continue;
        }
        var m = card.match(postLink);
        if (m !== null) {
            return m[1];
        }
    }
    return "";
}

function panelPercent(html, panel) {
    var m = html.match(new RegExp('data-testid="probability-ring-' + panel + '"[^>]*'));
    if (m === null) {
        return null;
    }
    var v = m[0].match(/data-target-value="(\d+)"/);
    return v === null ? null : parseInt(v[1], 10);
}
