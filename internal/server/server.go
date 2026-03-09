package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

// Server wraps the HTTP server and its dependencies.
type Server struct {
	httpServer *http.Server
	handler    http.Handler
	logger     *slog.Logger
}

// New creates a new Server with the given config and logger.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	router := NewRouter(cfg, nil, nil, nil, nil, nil, nil, nil, nil)

	s := &Server{
		handler: router,
		logger:  logger,
	}

	s.httpServer = &http.Server{
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// Start begins listening on the given address. It blocks until the server stops.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.logger.Info("server listening", "addr", addr)
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the server with a deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// ServeHTTP implements http.Handler, useful for testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
