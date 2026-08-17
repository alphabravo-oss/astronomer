// Command flux-fixture-server serves the local Git and Helm fixtures used by
// the disposable Flux integration test. It contains no credentials and is not
// part of an Astronomer runtime image.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxGitRequestBytes = 2 << 20

func main() {
	var (
		listenAddr = flag.String("listen", "127.0.0.1:0", "TCP address on which to listen")
		root       = flag.String("root", "", "fixture root containing git/ and helm/")
		portFile   = flag.String("port-file", "", "optional file to receive the selected TCP port")
		selfTest   = flag.Bool("self-test", false, "exit successfully without starting a server")
	)
	flag.Parse()
	if *selfTest {
		fmt.Println("flux-fixture-server: OK")
		return
	}
	if strings.TrimSpace(*root) == "" {
		log.Fatal("--root is required")
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := newFixtureHandler(absRoot, log.Default())
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	if *portFile != "" {
		port := listener.Addr().(*net.TCPAddr).Port
		if err := os.WriteFile(*portFile, []byte(fmt.Sprintf("%d\n", port)), 0o600); err != nil {
			_ = listener.Close()
			log.Fatal(err)
		}
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("serving synthetic Flux fixtures on %s", listener.Addr())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newFixtureHandler(root string, logger *log.Logger) (http.Handler, error) {
	for _, directory := range []string{"git", "helm"} {
		info, err := os.Stat(filepath.Join(root, directory))
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("fixture %s directory is unavailable", directory)
		}
	}
	gitExecPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return nil, fmt.Errorf("locate git-http-backend: %w", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(gitExecPath)), "git-http-backend")
	if info, err := os.Stat(backend); err != nil || info.IsDir() {
		return nil, fmt.Errorf("git-http-backend is unavailable")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	gitHandler := &cgi.Handler{
		Path:   backend,
		Root:   "/git",
		Env:    []string{"GIT_HTTP_EXPORT_ALL=true", "GIT_PROJECT_ROOT=" + filepath.Join(root, "git")},
		Logger: logger,
		Stderr: os.Stderr,
	}
	mux.Handle("/git/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBytes)
		gitHandler.ServeHTTP(w, r)
	}))
	mux.Handle("/helm/", http.StripPrefix("/helm/", http.FileServer(http.Dir(filepath.Join(root, "helm")))))
	return mux, nil
}
