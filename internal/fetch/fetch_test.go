package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fetch errors end up in the archiver's log; the token and API key in the
// query string must not.
func TestFetchErrorsCarryNoSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	url := srv.URL + "/hash/file?download=true&token=SECRET-TOKEN&api-key=SECRET-KEY"
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, ctx := range map[string]context.Context{"bad status": context.Background(), "client gone": canceled} {
		_, err := HTTP{Client: srv.Client()}.Fetch(ctx, url, 0, -1)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: error leaks the query: %v", name, err)
		}
		if !strings.Contains(err.Error(), "/hash/file") {
			t.Errorf("%s: error lost the path: %v", name, err)
		}
	}
}
