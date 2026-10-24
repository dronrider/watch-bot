// Источник: JSON Feed https://clauderesets.com/feed.json.
// Разбор консервативный: без items или без дат в items события не
// собираются, а причина пишется в лог хоста.

function collect() {
    var resp = http.get("https://clauderesets.com/feed.json");
    if (resp.status !== 200) {
        log("clauderesets: ответ " + resp.status + ", событий нет");
        return [];
    }
    var feed;
    try {
        feed = JSON.parse(resp.body);
    } catch (e) {
        log("clauderesets: тело не JSON: " + e);
        return [];
    }
    if (!isObject(feed) || !isArray(feed.items)) {
        log("clauderesets: формат не JSON Feed, items нет");
        return [];
    }
    var events = [];
    for (var i = 0; i < feed.items.length; i++) {
        var item = feed.items[i];
        if (!isObject(item)) {
            continue;
        }
        var id = strField(item, "id");
        var when = toRFC3339(strField(item, "date_published"));
        if (!id || !when) {
            continue;
        }
        events.push({
            id: "clauderesets:" + id,
            title: strField(item, "title") || strField(item, "summary") || "обновление",
            time: when,
            kind: kindOf(strField(item, "title") + " " + strField(item, "content_text")),
            url: strField(item, "url"),
            note: strField(item, "content_text")
        });
    }
    return events;
}

function isObject(v) {
    return v !== null && typeof v === "object";
}

function isArray(v) {
    return v !== null && typeof v === "object" && typeof v.length === "number";
}

function strField(obj, key) {
    var v = obj[key];
    if (typeof v === "string") {
        return v;
    }
    if (typeof v === "number") {
        return String(v);
    }
    return "";
}

function toRFC3339(s) {
    if (!s) {
        return null;
    }
    try {
        return new Date(s).toISOString();
    } catch (e) {
        return null;
    }
}

// kind выводится из текста события по ключевым словам, приоритет у более
// конкретных видов, всё нераспознанное остаётся намёком.
function kindOf(text) {
    var t = String(text).toLowerCase();
    if (t.indexOf("подня") >= 0 || t.indexOf("raise") >= 0 || t.indexOf("увелич") >= 0) {
        return "limit_raise";
    }
    if (t.indexOf("прогноз") >= 0 || t.indexOf("forecast") >= 0 || t.indexOf("ожидает") >= 0) {
        return "forecast";
    }
    if (t.indexOf("обеща") >= 0 || t.indexOf("promise") >= 0) {
        return "promise";
    }
    if (t.indexOf("сброс") >= 0 || t.indexOf("reset") >= 0 || t.indexOf("обнулен") >= 0) {
        return "reset";
    }
    return "hint";
}
