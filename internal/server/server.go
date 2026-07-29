package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/shinderuman/local-web-app-server/internal/config"
	"github.com/shinderuman/local-web-app-server/internal/webui"
)

type AppStatus struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	URL     string        `json:"url"`
	Backend BackendStatus `json:"backend"`
}

type Server struct {
	logger   *slog.Logger
	apps     []config.App
	backends map[string]*backend
	handler  http.Handler
}

func New(apps []config.App, runtimeDirectory, logDirectory string, logger *slog.Logger) *Server {
	server := &Server{
		logger:   logger,
		apps:     apps,
		backends: make(map[string]*backend, len(apps)),
	}
	for _, app := range apps {
		server.backends[app.Manifest.ID] = newBackend(app, runtimeDirectory, logDirectory, logger)
	}
	server.handler = server.routes()
	return server
}

func (server *Server) StartBackends(ctx context.Context) {
	var waitGroup sync.WaitGroup
	for _, app := range server.apps {
		current := server.backends[app.Manifest.ID]
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := current.Start(ctx); err != nil {
				server.logger.Error("app backend unavailable", "app", current.app.Manifest.ID, "error", err)
			}
		}()
	}
	waitGroup.Wait()
}

func (server *Server) StopBackends() error {
	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, len(server.backends))
	for _, current := range server.backends {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := current.Stop(); err != nil {
				errorsChannel <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errorsChannel)
	var result error
	for err := range errorsChannel {
		result = errors.Join(result, err)
	}
	return result
}

func (server *Server) Handler() http.Handler {
	return server.handler
}

func (server *Server) Statuses() []AppStatus {
	statuses := make([]AppStatus, 0, len(server.apps))
	for _, app := range server.apps {
		statuses = append(statuses, AppStatus{
			ID:      app.Manifest.ID,
			Name:    app.Manifest.Name,
			URL:     "/apps/" + app.Manifest.ID + "/",
			Backend: server.backends[app.Manifest.ID].Status(),
		})
	}
	return statuses
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", webui.Handler())
	mux.HandleFunc("/_local/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/_local/apps", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, server.Statuses())
	})
	mux.HandleFunc("/apps/", server.serveApp)
	return securityHeaders(mux)
}

func (server *Server) serveApp(writer http.ResponseWriter, request *http.Request) {
	trimmed := strings.TrimPrefix(request.URL.Path, "/apps/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	appID := parts[0]
	var app *config.App
	for index := range server.apps {
		if server.apps[index].Manifest.ID == appID {
			app = &server.apps[index]
			break
		}
	}
	if app == nil {
		http.NotFound(writer, request)
		return
	}
	remainder := "/"
	if len(parts) == 2 {
		remainder += parts[1]
	}
	if remainder == "/api" || strings.HasPrefix(remainder, "/api/") {
		cloned := request.Clone(request.Context())
		cloned.URL.Path = path.Clean(remainder)
		if strings.HasSuffix(remainder, "/") && !strings.HasSuffix(cloned.URL.Path, "/") {
			cloned.URL.Path += "/"
		}
		cloned.URL.RawPath = ""
		server.backends[appID].ServeHTTP(writer, cloned)
		return
	}
	cloned := request.Clone(request.Context())
	cloned.URL.Path = remainder
	cloned.URL.RawPath = ""
	staticHandler{root: app.WebRoot}.ServeHTTP(writer, cloned)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; script-src 'self'; style-src 'self'")
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
