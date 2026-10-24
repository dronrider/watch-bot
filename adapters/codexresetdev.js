// Источник: https://codexreset.dev/api/history. Формат сверен с боевой выдачей
// 2026-10-24: объект с массивом events, у записи поля id, title, effective_at
// или announced_at, source_url, summary. Разбор остаётся консервативным:
// чужой формат не собирает событий, а пишет причину в лог хоста.

function collect() {
    var resp = http.get("https://codexreset.dev/api/history");
    if (resp.status !== 200) {
        log("codexreset.dev: ответ " + resp.status + ", событий нет");
        return [];
    }
    var data;
    try {
        data = JSON.parse(resp.body);
    } catch (e) {
        log("codexreset.dev: тело не JSON: " + e);
        return [];
    }
    var list = null;
    if (isArray(data)) {
        list = data;
    } else if (isObject(data)) {
        var keys = ["events", "resets", "items", "updates"];
        for (var i = 0; i < keys.length; i++) {
            if (isArray(data[keys[i]])) {
                list = data[keys[i]];
                break;
            }
        }
    }
    if (!isArray(list)) {
        log("codexreset.dev: формат ответа не распознан, событий нет");
        return [];
    }
    var events = [];
    for (var j = 0; j < list.length; j++) {
        var item = list[j];
        if (!isObject(item)) {
            continue;
        }
        var id = strField(item, "id") || strField(item, "key");
        var when = itemTime(item);
        if (!id || !when) {
            continue;
        }
        var title = strField(item, "title") || strField(item, "message") || "обновление";
        var note = strField(item, "summary") || strField(item, "note") || strField(item, "detail");
        events.push({
            id: "codexresetdev:" + id,
            title: title,
            time: when,
            kind: kindOf(title + " " + note),
            url: strField(item, "source_url") || strField(item, "url"),
            note: note
        });
    }
    return events;
}

function itemTime(item) {
    var keys = ["effective_at", "announced_at", "time", "date", "at", "timestamp", "when"];
    for (var i = 0; i < keys.length; i++) {
        var v = item[keys[i]];
        if (typeof v === "number") {
            // unix-секунды и миллисекунды различаем по величине.
            var ms = v > 100000000000 ? v : v * 1000;
            try {
                return new Date(ms).toISOString();
            } catch (e) {
                continue;
            }
        }
        if (typeof v === "string" && v) {
            try {
                var iso = new Date(v).toISOString();
                if (iso) {
                    return iso;
                }
            } catch (e2) {
                continue;
            }
        }
    }
    return null;
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
