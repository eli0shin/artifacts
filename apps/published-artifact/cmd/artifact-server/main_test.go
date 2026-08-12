package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestPublicBaseURLIsRequired(t *testing.T) {
	t.Setenv("ARTIFACT_PUBLIC_BASE_URL", "")
	defer func() {
		if recover() == nil {
			t.Fatal("mustGetenv did not panic")
		}
	}()
	mustGetenv("ARTIFACT_PUBLIC_BASE_URL")
}

func TestHTTPServerBoundsTotalUploadTime(t *testing.T) {
	t.Setenv("ARTIFACT_UPLOAD_TIMEOUT", "1h")

	server, err := newHTTPServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	if server.ReadTimeout != time.Hour {
		t.Fatalf("ReadTimeout = %s, want %s", server.ReadTimeout, time.Hour)
	}
}

func TestHTTPServerBoundsTotalPublicationTime(t *testing.T) {
	t.Setenv("ARTIFACT_UPLOAD_TIMEOUT", "50ms")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		http.Error(w, "timed out", http.StatusRequestTimeout)
	})
	server, err := newHTTPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()

	response, err := http.Post("http://"+listener.Addr().String()+"/api/v1/artifacts", "application/x-tar", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusRequestTimeout)
	}
}

func TestHTTPServerRejectsInvalidUploadTimeout(t *testing.T) {
	for _, value := range []string{"not-a-duration", "0", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ARTIFACT_UPLOAD_TIMEOUT", value)
			if _, err := newHTTPServer(http.NotFoundHandler()); err == nil {
				t.Fatalf("newHTTPServer accepted upload timeout %q", value)
			}
		})
	}
}
