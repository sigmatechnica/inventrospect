// Inventrospect MCP is an optional REST-backed adapter; stdout is protocol-only.
package main

import (
	"context"
	"flag"
	"fmt"
	"inventrospect/internal/buildinfo"
	"inventrospect/internal/operational"
	"inventrospect/internal/serverlife"
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
	shutdownTimeout := flag.Duration("shutdown-timeout", 15*time.Second, "Maximum wait for in-flight HTTP requests during shutdown")
	version := flag.Bool("version", false, "Print build version and exit")
	allowInsecure := flag.Bool("allow-insecure-http", false, "Explicitly allow non-loopback HTTP (private container network or deliberately insecure LAN)")
	externalHTTPS := flag.Bool("external-https", false, "Listener is protected by a trusted HTTPS reverse proxy")
	flag.Parse()
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	if *version {
		fmt.Println(buildinfo.String())
		return
	}
	log.Printf("startup %s", buildinfo.String())
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
	if e = operational.ValidateListen(*addr, *externalHTTPS, *allowInsecure); e != nil {
		log.Fatal(e)
	}
	health := &operational.Health{Check: func(ctx context.Context) error { return operational.Probe(ctx, backend.BaseURL+"/readyz") }}
	srv := &http.Server{Addr: *addr, Handler: health.Handler(mcpserver.HTTP(*base)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second}

	log.Printf("MCP Streamable HTTP listening on %s/mcp; credentials come from each request", *addr)
	if e = serverlife.Run(ctx, srv, *shutdownTimeout, srv.ListenAndServe, health.Drain); e != nil {
		log.Fatal(e)
	}
	log.Print("shutdown complete")
}
