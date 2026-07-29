package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	socketPath := os.Getenv("LOCAL_WEB_SOCKET")
	if socketPath == "" {
		panic("LOCAL_WEB_SOCKET is required")
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		panic(err)
	}
	defer os.Remove(socketPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/echo", func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"path": request.URL.Path,
			"app":  os.Getenv("DUMMY_APP_NAME"),
		})
	})
	mux.HandleFunc("/api/events", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: ready\n\n")
		writer.(http.Flusher).Flush()
	})

	server := &http.Server{Handler: mux}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		_ = server.Close()
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}
