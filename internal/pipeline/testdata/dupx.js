// Тестовый адаптер: без похода в сеть. По флагу mode=1 возвращает событие
// со ссылкой на пост X в заметке, как лента modelyard.
function collect() {
    if (state.get("mode") !== "1") {
        return [];
    }
    return [{
        id: "dupx:1",
        title: "Tibo says reset fully propagated",
        time: "2026-10-25T06:06:16Z",
        kind: "reset",
        url: "https://tibo.modelyard.dev/events/132",
        note: "Source: https://x.com/thsottiaux/status/2114237083652882932"
    }];
}
