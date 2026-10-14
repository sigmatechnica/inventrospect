package serverlife

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"inventrospect/internal/inventory"
)

type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestDrainBeforeDatabaseClose(t *testing.T) {
	s, e := inventory.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	checked := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, e := s.HasUsers(r.Context())
		checked <- e
		w.Write([]byte("finished"))
	})}
	listener := &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		err := Run(ctx, srv, 2*time.Second, func() error { return srv.Serve(listener) })
		s.Close()
		finished <- err
	}()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		a, b := net.Pipe()
		select {
		case listener.conns <- a:
			return b, nil
		case <-ctx.Done():
			a.Close()
			b.Close()
			return nil, ctx.Err()
		}
	}}}
	defer client.CloseIdleConnections()
	response := make(chan error, 1)
	go func() {
		r, e := client.Get("http://inventory/")
		if e == nil {
			body, err := io.ReadAll(r.Body)
			r.Body.Close()
			e = err
			if string(body) != "finished" {
				e = errors.New("incomplete response")
			}
		}
		response <- e
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case <-listener.done:
	case <-time.After(time.Second):
		t.Fatal("listener not closed")
	}
	select {
	case err := <-finished:
		t.Fatal("returned before request completed", err)
	default:
	}
	close(release)
	if e := <-checked; e != nil {
		t.Fatal("database closed during drain", e)
	}
	if e := <-response; e != nil {
		t.Fatal(e)
	}
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if _, e := s.HasUsers(context.Background()); e == nil {
		t.Fatal("database not closed after drain")
	}
}

func TestShutdownTimeoutCancelsConnections(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(exited) })}
	listener := &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, srv, 10*time.Millisecond, func() error { return srv.Serve(listener) }) }()
	client, server := net.Pipe()
	defer client.Close()
	listener.conns <- server
	go func() { io.WriteString(client, "GET / HTTP/1.1\r\nHost: inventory\r\n\r\n") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout did not stop server")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("connection context not cancelled")
	}
}
