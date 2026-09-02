//go:generate go run ./main.go generate-schemas ../../../../
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/docopt/docopt-go"
	"github.com/orbit-online/s3-presigned-url-redirector/redirector"
	"golang.org/x/sync/errgroup"
)

type Params struct {
	Serve       bool     `docopt:"serve"`
	Bucket      string   `docopt:"BUCKET"`
	Methods     []string `docopt:"--method"`
	TTL         string   `docopt:"--ttl"`
	Addr        string   `docopt:"--addr"`
	MetricsAddr string   `docopt:"--metrics"`
	ProbesAddr  string   `docopt:"--probes"`
}

func main() {
	slog.SetDefault(slog.Default())
	switch os.Getenv("LOGLEVEL") {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "verbose":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "warning":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	}
	parser, err := docopt.ParseDoc(`s3-presigned-url-redirector - Redirect S3 requests to presigned URLs
Usage:
  redirector serve BUCKET [--method METHOD...] [options]

Options:
  --ttl SECONDS       Expiry of the signed URLs in seconds [default: 900]
  --addr ADDR         Address to listen on [default: :3000]
  --metrics ADDR      Address to serve metrics on, "" to disable [default: :3001]
  --probes ADDR       Address to serve probes on, "" to disable [default: :3002]
  -m --method METHOD  Methods to whitelist for signing [default: HEAD GET]
`)
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	params := Params{}
	err = parser.Bind(&params)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	if params.Serve {
		err = serve(context.Background(), params)
	}
	if err != nil {
		os.Stderr.WriteString(err.Error())
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func serve(parentCtx context.Context, params Params) error {
	signalCtx, stop := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ttl, err := strconv.Atoi(params.TTL)
	if err != nil {
		return err
	}
	wg, ctx := errgroup.WithContext(signalCtx)
	wg.Go(func() error {
		return redirector.Serve(ctx, params.Addr, params.Methods, params.Bucket, time.Duration(ttl)*time.Second)
	})
	if params.MetricsAddr != "" {
		wg.Go(func() error { return redirector.ServeMetrics(ctx, params.MetricsAddr) })
	}
	if params.ProbesAddr != "" {
		wg.Go(func() error { return redirector.ServeProbes(ctx, params.ProbesAddr) })
	}
	return wg.Wait()
}
