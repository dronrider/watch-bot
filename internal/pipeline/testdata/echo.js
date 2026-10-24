// Тестовый адаптер: всегда возвращает одно и то же событие без похода в сеть.
function collect() {
    return [{
        id: "echo:1",
        title: "Сброс лимитов",
        time: "2026-10-20T00:00:00Z",
        kind: "reset",
        url: "https://example.test/reset",
        note: "обнуление в 00:00 UTC"
    }];
}
