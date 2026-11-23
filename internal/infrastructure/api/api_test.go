package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExecuteRequestKeepsUnderlyingErrorOnConnectionFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	ctx := context.Background()
	headers := map[string]string{}

	apiError := executeRequest(&ctx, http.MethodGet, "http://"+address+"/status", time.Second, &headers, nil, nil)

	if apiError == nil {
		t.Fatal("expected an error for a closed port")
	}
	if apiError.Status != 0 {
		t.Fatalf("expected status 0 for a connection failure, got %d", apiError.Status)
	}
	if apiError.Err == nil {
		t.Fatal("expected the underlying error to be kept")
	}
}

func TestExecuteRequestReturnsStatusOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx := context.Background()
	headers := map[string]string{}

	apiError := executeRequest(&ctx, http.MethodGet, server.URL, time.Second, &headers, nil, nil)

	if apiError == nil || apiError.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %+v", apiError)
	}
	if apiError.Err != nil {
		t.Fatalf("expected no transport error, got %v", apiError.Err)
	}
}
