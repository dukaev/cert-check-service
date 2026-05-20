package handler_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServer_ShutdownWaitsForInflight verifies the property that backs our
// graceful shutdown story in main.go: srv.Shutdown blocks until in-flight
// requests complete. If we ever replace srv.ListenAndServe with something
// that bypasses Shutdown (e.g. os.Exit on signal), this test fails.
func TestServer_ShutdownWaitsForInflight(t *testing.T) {
	inFlight := make(chan struct{})
	release := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// Fire a slow request and wait until the handler is actually executing.
	done := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			done <- -1
			return
		}
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	<-inFlight

	// Shutdown must block until the in-flight request finishes.
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(context.Background()) }()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before in-flight request completed (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
		// expected: Shutdown is waiting
	}

	// Let the handler finish — both the client and Shutdown must now complete.
	close(release)

	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Errorf("in-flight request status = %d, want 200", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not complete after release")
	}

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("Shutdown err = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after in-flight request completed")
	}
}

// TestServer_ShutdownRejectsNewConnections — after Shutdown returns, new
// requests must be refused.
func TestServer_ShutdownRejectsNewConnections(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()

	// Smoke-check the server is up.
	resp, err := http.Get("http://" + addr + "/ok")
	if err != nil {
		t.Fatalf("pre-shutdown request failed: %v", err)
	}
	resp.Body.Close()

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown err = %v", err)
	}

	// Disable connection reuse so we hit the closed listener, not a half-open keep-alive.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: time.Second}
	resp2, err := client.Get("http://" + addr + "/ok")
	if err == nil {
		resp2.Body.Close()
		t.Fatal("expected error connecting to shut-down server, got nil")
	}
}
