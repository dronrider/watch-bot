package harness

import (
	"net/http"
	"net/http/httptest"
)

// fakeFlaky дважды отдаёт 500 и на третий раз 200.
type fakeFlaky struct {
	hits int
}

func newFlakyServer(f *fakeFlaky) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.hits++
		if f.hits < 3 {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte("ok"))
	}))
}
