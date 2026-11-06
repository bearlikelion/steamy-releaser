package steam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("appids") {
		case "480":
			_, _ = w.Write([]byte(`{"480":{"success":true,"data":{"name":"Spacewar"}}}`))
		default:
			_, _ = w.Write([]byte(`{"1":{"success":false}}`))
		}
	}))
	defer server.Close()
	previous := storeAPIBase
	storeAPIBase = server.URL
	defer func() { storeAPIBase = previous }()
	if name, err := AppName(context.Background(), 480); err != nil || name != "Spacewar" {
		t.Fatalf("AppName(480) = %q, %v", name, err)
	}
	if name, err := AppName(context.Background(), 1); err != nil || name != "" {
		t.Fatalf("AppName(1) = %q, %v", name, err)
	}
}
