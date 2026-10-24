package harness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dop251/goja"
)

// UserAgent общий для всех походов за источниками, чтобы владелец сайта
// видел одного и того же клиента.
const UserAgent = "resetwatch/0.1"

// Response это результат http.get для скрипта.
type Response struct {
	Status      int
	ContentType string
	Body        string
}

// GetOptions приходит из второго аргумента http.get.
type GetOptions struct {
	Timeout time.Duration
	Headers map[string]string
}

// HTTPClient ходит за источником. Реальная реализация ретраит сама,
// подмены в тестах отвечают без сети.
type HTTPClient interface {
	Get(ctx context.Context, url string, opts GetOptions) (Response, error)
}

// RealHTTP это боевой клиент: таймаут на запрос, общий User-Agent и
// повторы с бэкоффом на сетевых ошибках и ответах от 500.
type RealHTTP struct {
	Attempts int
	Backoff  time.Duration
	Client   *http.Client
}

func NewRealHTTP() *RealHTTP {
	return &RealHTTP{
		Attempts: 3,
		Backoff:  2 * time.Second,
		Client:   &http.Client{},
	}
}

func (h *RealHTTP) Get(ctx context.Context, url string, opts GetOptions) (Response, error) {
	attempts := h.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(h.Backoff * time.Duration(attempt)):
			}
		}
		resp, err := h.once(ctx, url, opts)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return Response{}, fmt.Errorf("источник %s: %d попыток: %w", url, attempts, lastErr)
}

func (h *RealHTTP) once(ctx context.Context, url string, opts GetOptions) (Response, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode >= 500 {
		return Response{}, fmt.Errorf("ответ %d от %s", resp.StatusCode, url)
	}
	return Response{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: string(body)}, nil
}

func jsHTTPGet(vm *goja.Runtime, ctx context.Context, deps Deps, call goja.FunctionCall) goja.Value {
	url := call.Argument(0).String()
	if url == "" {
		panic(vm.NewGoError(fmt.Errorf("http.get: пустой url")))
	}
	opts := GetOptions{}
	if len(call.Arguments) > 1 {
		opts = getOptions(vm, call.Argument(1))
	}
	resp, err := deps.HTTP.Get(ctx, url, opts)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return vm.ToValue(map[string]interface{}{
		"status":      resp.Status,
		"contentType": resp.ContentType,
		"body":        resp.Body,
	})
}

func getOptions(vm *goja.Runtime, arg goja.Value) GetOptions {
	opts := GetOptions{}
	if goja.IsUndefined(arg) || goja.IsNull(arg) {
		return opts
	}
	obj := arg.ToObject(vm)
	if t := obj.Get("timeout_ms"); t != nil && !goja.IsUndefined(t) {
		ms := t.ToInteger()
		if ms > 0 {
			opts.Timeout = time.Duration(ms) * time.Millisecond
		}
	}
	if hs := obj.Get("headers"); hs != nil && !goja.IsUndefined(hs) && !goja.IsNull(hs) {
		ho := hs.ToObject(vm)
		opts.Headers = map[string]string{}
		for _, key := range ho.Keys() {
			opts.Headers[key] = ho.Get(key).String()
		}
	}
	return opts
}
