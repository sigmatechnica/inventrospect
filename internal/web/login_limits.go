package web

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

type loginWindow struct {
	Started  time.Time
	Attempts int
}

const maxLoginEntries = 4096

// RemoteAddr is the directly connected peer. Forwarded headers are intentionally
// ignored: deployments behind a proxy share that proxy's IP allowance.
func (s *Server) allowLogin(r *http.Request, username string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	if s.logins == nil {
		s.logins = map[string]loginWindow{}
	}
	for key, w := range s.logins {
		if now.Sub(w.Started) >= time.Minute {
			delete(s.logins, key)
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	keys := []string{"ip:" + ip, fmt.Sprintf("account:%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username)))))}
	limits := []int{30, 10}
	for j, key := range keys {
		if s.logins[key].Attempts >= limits[j] {
			return false
		}
	}
	for _, key := range keys {
		w, exists := s.logins[key]
		if !exists {
			if len(s.logins) >= maxLoginEntries {
				oldest := ""
				var at time.Time
				for k, v := range s.logins {
					if oldest == "" || v.Started.Before(at) {
						oldest, at = k, v.Started
					}
				}
				delete(s.logins, oldest)
			}
			w.Started = now
		}
		w.Attempts++
		s.logins[key] = w
	}
	return true
}
