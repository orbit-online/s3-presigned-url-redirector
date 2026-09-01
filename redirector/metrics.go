package redirector

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func ServeMetrics(ctx context.Context, addr string) error {
	serveMux := http.NewServeMux()
	serveMux.Handle("/metrics", promhttp.Handler())
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
