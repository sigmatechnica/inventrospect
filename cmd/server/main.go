package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"inventrospect/internal/buildinfo"
	"inventrospect/internal/inventory"
	"inventrospect/internal/operational"
	"inventrospect/internal/serverlife"
	"inventrospect/internal/web"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	data := flag.String("data", "./data", "Persistent data directory")
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	initAdmin := flag.Bool("init-admin", false, "Create the initial admin from INVENTROSPECT_ADMIN_USER and INVENTROSPECT_ADMIN_PASSWORD, then exit")
	secure := flag.Bool("secure-cookies", false, "Require HTTPS for session cookies (use behind an HTTPS reverse proxy)")
	maxBulk := flag.Int("max-bulk-records", 100, "Maximum records per mutating bulk request (1–100)")
	writeRate := flag.Int("agent-writes-per-minute", 0, "Optional ceiling on token write units per minute (0 uses token settings)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 15*time.Second, "Maximum wait for in-flight HTTP requests during shutdown")
	version := flag.Bool("version", false, "Print build version and exit")
	allowInsecure := flag.Bool("allow-insecure-http", false, "Explicitly allow non-loopback HTTP (private container network or deliberately insecure LAN)")
	passwordStdin := flag.Bool("admin-password-stdin", false, "Read bootstrap password from standard input")
	flag.Parse()
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	if *version {
		fmt.Println(buildinfo.String())
		return
	}
	log.Printf("startup %s", buildinfo.String())
	s, e := inventory.Open(*data)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	if e = s.ConfigureLimits(*maxBulk, *writeRate); e != nil {
		log.Fatal(e)
	}
	ctx := context.Background()
	if *initAdmin {
		username := os.Getenv("INVENTROSPECT_ADMIN_USER")
		if username == "" {
			username = "admin"
		}
		password := os.Getenv("INVENTROSPECT_ADMIN_PASSWORD")
		if *passwordStdin {
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				log.Fatal("Cannot read bootstrap password line from stdin")
			}
			password = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		}
		if e = s.Bootstrap(ctx, username, password); e != nil {
			log.Fatal(e)
		}
		fmt.Println("Initial admin created.")
		return
	}
	ok, e := s.HasUsers(ctx)
	if e != nil {
		log.Fatal(e)
	}
	if !ok {
		log.Fatal("No users exist. First run with -init-admin and INVENTROSPECT_ADMIN_PASSWORD (12–72 bytes).")
	}
	if e = operational.ValidateListen(*addr, *secure, *allowInsecure); e != nil {
		log.Fatal(e)
	}
	health := &operational.Health{Check: s.Ready}
	srv := &http.Server{Addr: *addr, Handler: health.Handler(web.New(s, *secure)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	stop, done := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer done()

	log.Printf("Inventrospect listening at http://%s (data: %s)", *addr, *data)
	if e = serverlife.Run(stop, srv, *shutdownTimeout, srv.ListenAndServe, health.Drain); e != nil {
		log.Fatal(e)
	}
	log.Print("shutdown complete")
}
