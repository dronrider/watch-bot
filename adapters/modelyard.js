// Источник: лента https://tibo.modelyard.dev/feed.xml через rss.read.
// Парсинг RSS и Atom делает го-хост, скрипт только выбирает события.

function collect() {
    var items = rss.read("https://tibo.modelyard.dev/feed.xml");
    var events = [];
    for (var i = 0; i < items.length; i++) {
        var item = items[i];
        if (!isObject(item)) {
            continue;
        }
        var id = strField(item, "link");
        var when = toRFC3339(strField(item, "published"));
        if (!id || !when) {
            continue;
        }
        var title = strField(item, "title") || "обновление";
        events.push({
            id: "modelyard:" + id,
            title: title,
            time: when,
            kind: kindOf(title + " " + strField(item, "summary")),
            url: id,
            note: strField(item, "summary")
        });
    }
    return events;
}

function isObject(v) {
    return v !== null && typeof v === "object";
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
