// Тестовый адаптер: без похода в сеть. По флагу mode=1 возвращает событие
// со ссылкой на тот же пост X в самом URL, как история codexreset.dev.
function collect() {
    if (state.get("mode") !== "1") {
        return [];
    }
    return [{
        id: "dupx2:1",
        title: "Codex global quota reset recorded",
        time: "2026-10-25T06:06:16Z",
        kind: "reset",
        url: "https://x.com/thsottiaux/status/2114237083652882932",
        note: "Timestamp from a Codex Resets observation."
    }];
}
