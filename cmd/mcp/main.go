// Inventrospect MCP is an optional REST-backed adapter; stdout is protocol-only.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"inventrospect/internal/mcpserver"
)

func main() {
	base := flag.String("base-url", "http://127.0.0.1:8080", "Existing inventory application URL")
	addr := flag.String("addr", "", "Optional Streamable HTTP listen address, e.g. 127.0.0.1:8081; otherwise use stdio")
	flag.Parse()
	backend, e := mcpserver.NewBackend(*base, os.Getenv("INVENTROSPECT_API_TOKEN"))
	if e != nil {
		log.Fatal(e)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *addr == "" {
		if e = backend.Check(ctx); e != nil {
			log.Fatal("Agent authentication failed: ", e)
		}
		if e = mcpserver.New(backend).Run(ctx, &mcp.StdioTransport{MaxLineLength: 36 * 1024 * 1024}); e != nil && ctx.Err() == nil {
			log.Fatal(e)
		}
		return
	}
	srv := &http.Server{Addr: *addr, Handler: mcpserver.HTTP(*base), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		srv.Shutdown(stop)
	}()
	log.Printf("MCP Streamable HTTP listening on %s/mcp; credentials come from each request", *addr)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
