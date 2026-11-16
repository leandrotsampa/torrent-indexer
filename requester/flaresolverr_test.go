package requester

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func Test_normalizeFlareSolverrURL(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "empty",
			address: "",
			want:    "",
		},
		{
			name:    "host only",
			address: "192.168.6.10",
			want:    "http://192.168.6.10:8191",
		},
		{
			name:    "host with scheme",
			address: "http://192.168.6.10",
			want:    "http://192.168.6.10:8191",
		},
		{
			name:    "host with scheme and port",
			address: "http://flaresolverr:8191",
			want:    "http://flaresolverr:8191",
		},
		{
			name:    "trailing slash",
			address: "http://flaresolverr:8191/",
			want:    "http://flaresolverr:8191",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeFlareSolverrURL(tt.address); got != tt.want {
				t.Fatalf("normalizeFlareSolverrURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeFlareSolverr answers the subset of the FlareSolverr v1 API used by Get.
func fakeFlareSolverr(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req["cmd"] {
		case "sessions.list":
			_, _ = w.Write([]byte(`{"status":"ok","sessions":[]}`))
		case "sessions.create":
			_, _ = w.Write([]byte(`{"status":"ok","session":"s1"}`))
		case "request.get":
			_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,"response":"<html><body>solved</body></html>"}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
}

func TestFlareSolverrGetInitializesLazilyWhenUnavailableAtStartup(t *testing.T) {
	// reserve a port nobody listens on, so the startup init fails
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	downURL := "http://" + ln.Addr().String()
	ln.Close()

	f := NewFlareSolverr(downURL, 5000, 1)
	if f.initiated {
		t.Fatal("expected init to fail while FlareSolverr is down")
	}

	srv := fakeFlareSolverr(t)
	defer srv.Close()
	f.url = srv.URL                 // FlareSolverr is up now
	f.lastInitAttempt = time.Time{} // don't wait for the retry backoff

	body, err := f.Get(context.Background(), "https://example.com/", 0)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got, _ := io.ReadAll(body)
	if !strings.Contains(string(got), "solved") {
		t.Fatalf("Get() body = %q, want the solved page", got)
	}
}

func TestFlareSolverrGetWithoutAddressReturnsEmpty(t *testing.T) {
	f := NewFlareSolverr("", 5000, 1)
	body, err := f.Get(context.Background(), "https://example.com/", 0)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got, _ := io.ReadAll(body)
	if len(got) != 0 {
		t.Fatalf("Get() body = %q, want empty", got)
	}
}
