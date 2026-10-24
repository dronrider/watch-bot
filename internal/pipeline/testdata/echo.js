// Тестовый адаптер: без похода в сеть возвращает одно событие, а по флагу
// mode=1 в kv добавляет второе, чтобы тест будил конвейер новым событием.
function collect() {
    var events = [{
        id: "echo:1",
        title: "Сброс лимитов",
        time: "2026-10-20T00:00:00Z",
        kind: "reset",
        url: "https://example.test/reset",
        note: "обнуление в 00:00 UTC"
    }];
    if (state.get("mode") === "1") {
        events.push({
            id: "echo:2",
            title: "Обещание сброса",
            time: "2026-10-21T00:00:00Z",
            kind: "promise",
            url: "",
            note: ""
        });
    }
    return events;
}
