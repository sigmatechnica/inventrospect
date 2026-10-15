package operational

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

type Health struct {
	draining atomic.Bool
	Check    func(context.Context) error
}

func (h *Health) Drain() { h.draining.Store(true) }
func (h *Health) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		if h.draining.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		if r.URL.Path == "/readyz" && h.Check != nil {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if h.Check(ctx) != nil {
				http.Error(w, "unavailable", 503)
				return
			}
		}
		w.WriteHeader(200)
		w.Write([]byte("ok\n"))
	})
}
func ValidateListen(addr string, secure, allowInsecure bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) || secure || allowInsecure {
		return nil
	}
	return fmt.Errorf("non-loopback HTTP requires HTTPS proxy configuration or explicit -allow-insecure-http")
}
func Probe(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health endpoint unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("health endpoint returned %d", res.StatusCode)
	}
	return nil
}
