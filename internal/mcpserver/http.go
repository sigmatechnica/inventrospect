package mcpserver

import (
	"context"
	"errors"
	"inventrospect/internal/inventory"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type backendKey struct{}

// HTTP uses a fresh, authenticated backend per request. No credential/session is
// shared between clients. MCP remains optional and holds no database connection.
func HTTP(base string) http.Handler { return httpHandler(base, nil) }
func httpHandler(base string, client *http.Client) http.Handler {
	sdk := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return New(r.Context().Value(backendKey{}).(*Backend)) }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 36 * 1024 * 1024})
	protected := http.NewCrossOriginProtection().Handler(sdk)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Origin") != "" { // Check before making even an authentication call upstream.
			if e := http.NewCrossOriginProtection().Check(r); e != nil {
				http.Error(w, "Cross-origin request denied", http.StatusForbidden)
				return
			}
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer inv_") {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Agent bearer token required", 401)
			return
		}
		b, e := NewBackend(base, strings.TrimPrefix(auth, "Bearer "))
		if e != nil {
			http.Error(w, "Invalid backend configuration", 500)
			return
		}
		if client != nil {
			b.HTTP = client
		}
		if e = b.Check(r.Context()); e != nil {
			var problem *inventory.Error
			if errors.As(e, &problem) && problem.Code == "unauthorized" {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "Invalid or revoked agent credential", 401)
			} else {
				http.Error(w, "Inventory API unavailable", 503)
			}
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 36*1024*1024)
		protected.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), backendKey{}, b)))
	})
}
