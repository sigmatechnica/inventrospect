package main

import (
	"context"
	"flag"
	"fmt"
	"inventrospect/internal/inventory"
	"inventrospect/internal/web"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	data := flag.String("data", "./data", "Persistent data directory")
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	initAdmin := flag.Bool("init-admin", false, "Create the initial admin from INVENTROSPECT_ADMIN_USER and INVENTROSPECT_ADMIN_PASSWORD, then exit")
	secure := flag.Bool("secure-cookies", false, "Require HTTPS for session cookies (use behind an HTTPS reverse proxy)")
	maxBulk := flag.Int("max-bulk-records", 100, "Maximum records per mutating bulk request (1–100)")
	writeRate := flag.Int("agent-writes-per-minute", 60, "Mutating requests per agent token per minute (0 disables)")
	flag.Parse()
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
		if e = s.Bootstrap(ctx, username, os.Getenv("INVENTROSPECT_ADMIN_PASSWORD")); e != nil {
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
	srv := &http.Server{Addr: *addr, Handler: web.New(s, *secure), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	stop, done := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer done()
	go func() {
		<-stop.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := srv.Shutdown(ctx); e != nil {
			log.Print(e)
		}
	}()
	log.Printf("Inventrospect listening at http://%s (data: %s)", *addr, *data)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
