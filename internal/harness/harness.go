// Песочница адаптеров: скрипт на JS под goja получает только логику похода
// за источником, расписание, состояние и отправку держит го-хост.
package harness

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Event это одно событие сброса из collect(). Время обязано быть RFC3339,
// kind берётся из закрытого словаря.
type Event struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Time  string `json:"time"`
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Note  string `json:"note"`
}

var kinds = map[string]bool{
	"hint":        true,
	"promise":     true,
	"reset":       true,
	"limit_raise": true,
	"forecast":    true,
}

// StateAccess это ключи адаптера, уже пришитые к его источнику.
type StateAccess interface {
	Get(key string) (string, bool, error)
	Set(key, value string) error
}

type Deps struct {
	HTTP  HTTPClient
	RSS   RSSClient
	State StateAccess
	Log   *slog.Logger
}

// Run читает скрипт с диска и прогоняет его collect() в новом рантайме:
// между прогонами ничего не живёт, поэтому адаптер не может держать состояние
// в глобальных переменных вместо state.
func Run(ctx context.Context, scriptPath string, deps Deps) ([]Event, error) {
	src, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", scriptPath, err)
	}
	vm := goja.New()
	// goja не даёт скрипту ничего из хоста, кроме выставленного ниже,
	// поэтому запрет на сеть и чужое состояние держится самим рантаймом.
	expose(vm, ctx, deps)
	if _, err := vm.RunString(string(src)); err != nil {
		return nil, fmt.Errorf("запуск %s: %w", scriptPath, err)
	}
	collect, ok := goja.AssertFunction(vm.Get("collect"))
	if !ok {
		return nil, fmt.Errorf("%s не определил функцию collect()", scriptPath)
	}
	res, err := collect(goja.Undefined())
	if err != nil {
		return nil, fmt.Errorf("collect() в %s: %w", scriptPath, err)
	}
	return decodeEvents(scriptPath, res)
}

func decodeEvents(scriptPath string, res goja.Value) ([]Event, error) {
	raw := res.Export()
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("collect() в %s должен вернуть массив", scriptPath)
	}
	events := make([]Event, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%s: событие №%d не объект", scriptPath, i+1)
		}
		e := Event{
			ID:    strField(m, "id"),
			Title: strField(m, "title"),
			Time:  strField(m, "time"),
			Kind:  strField(m, "kind"),
			URL:   strField(m, "url"),
			Note:  strField(m, "note"),
		}
		if e.ID == "" {
			return nil, fmt.Errorf("%s: у события №%d пустой id", scriptPath, i+1)
		}
		if e.Title == "" {
			return nil, fmt.Errorf("%s: у события %s пустой title", scriptPath, e.ID)
		}
		if !kinds[e.Kind] {
			return nil, fmt.Errorf("%s: у события %s kind %q вне словаря", scriptPath, e.ID, e.Kind)
		}
		if _, err := time.Parse(time.RFC3339, e.Time); err != nil {
			return nil, fmt.Errorf("%s: у события %s время %q не RFC3339", scriptPath, e.ID, e.Time)
		}
		events = append(events, e)
	}
	return events, nil
}

func strField(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func expose(vm *goja.Runtime, ctx context.Context, deps Deps) {
	httpObj := vm.NewObject()
	httpObj.Set("get", func(call goja.FunctionCall) goja.Value {
		return jsHTTPGet(vm, ctx, deps, call)
	})
	vm.Set("http", httpObj)

	rssObj := vm.NewObject()
	rssObj.Set("read", func(call goja.FunctionCall) goja.Value {
		return jsRSSRead(vm, ctx, deps, call)
	})
	vm.Set("rss", rssObj)

	stateObj := vm.NewObject()
	stateObj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		value, found, err := deps.State.Get(key)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("state.get(%s): %w", key, err)))
		}
		if !found {
			return goja.Null()
		}
		return vm.ToValue(value)
	})
	stateObj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		value := call.Argument(1).String()
		if err := deps.State.Set(key, value); err != nil {
			panic(vm.NewGoError(fmt.Errorf("state.set(%s): %w", key, err)))
		}
		return goja.Undefined()
	})
	vm.Set("state", stateObj)

	vm.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			parts = append(parts, arg.String())
		}
		if deps.Log != nil {
			deps.Log.Info(strings.Join(parts, " "))
		}
		return goja.Undefined()
	})
}
