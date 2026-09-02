package redirector

import (
	"context"
	"net/http"
)

var probesReady = false
var probesHealthy = false

func ServeProbes(ctx context.Context, addr string) error {
	serveMux := http.NewServeMux()
	serveMux.Handle("/startup", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	serveMux.Handle("/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probesReady {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	serveMux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probesHealthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	cleartextServer := http.Server{Addr: addr, Handler: serveMux}
	serveErr := make(chan error)
	go func() { serveErr <- cleartextServer.ListenAndServe() }()
	select {
	case <-ctx.Done():
		return cleartextServer.Shutdown(ctx)
	case err := <-serveErr:
		return err
	}
}
