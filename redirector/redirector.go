package redirector

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

func Serve(ctx context.Context, addr string, allowedMethods []string, bucket string, ttl time.Duration, proxyPuts bool) error {
	methodCounter := promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "s3_presigned_url_redirector",
		Name:      "requests_total",
		Help:      "Number of requests received",
	}, prometheus.UnconstrainedLabels{"method"})
	proxiedCounter := promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "s3_presigned_url_redirector",
		Name:      "put_request_proxied",
		Help:      "Number of PUT requests proxied",
	})
	proxiedBytes := promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "s3_presigned_url_redirector",
		Name:      "put_bytes_proxied",
		Help:      "Number of bytes proxied for PUT requests without 'Expect: 100-continue' header",
	})
	deniedCounter := promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "s3_presigned_url_redirector",
		Name:      "requests_denied",
		Help:      "Number of requests denied because of whitelisting",
	}, prometheus.UnconstrainedLabels{"method"})
	errorsCounter := promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "s3_presigned_url_redirector",
		Name:      "errors_total",
		Help:      "Number of errors encountered",
	}, prometheus.UnconstrainedLabels{"errortype"})

	knownMethods := []string{"HEAD", "GET", "PUT", "DELETE"}
	var invalid []string
	for _, method := range allowedMethods {
		if !slices.Contains(knownMethods, method) {
			invalid = append(invalid, method)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("The following method(s) are unknown and cannot be whitelisted: %s", invalid)
	}

	config, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	presigner := s3.NewPresignClient(s3.NewFromConfig(config))
	signReq := func(method string, key string) (*v4.PresignedHTTPRequest, error) {
		methodCounter.WithLabelValues(method).Inc()
		switch method {
		case "HEAD":
			return presigner.PresignHeadObject(ctx, &s3.HeadObjectInput{
				Bucket: &bucket,
				Key:    &key,
			}, s3.WithPresignExpires(ttl))
		case "GET":
			return presigner.PresignGetObject(ctx, &s3.GetObjectInput{
				Bucket: &bucket,
				Key:    &key,
			}, s3.WithPresignExpires(ttl))
		case "PUT":
			return presigner.PresignPutObject(ctx, &s3.PutObjectInput{
				Bucket: &bucket,
				Key:    &key,
			}, s3.WithPresignExpires(ttl))
		case "DELETE":
			return presigner.PresignDeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: &bucket,
				Key:    &key,
			}, s3.WithPresignExpires(ttl))
		default:
			errorsCounter.WithLabelValues("unsupported-method").Inc()
			return nil, fmt.Errorf("Unsupported method %s", method)
		}
	}

	serveMux := http.NewServeMux()
	serveMux.Handle("/{path...}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !slices.Contains(allowedMethods, req.Method) {
			deniedCounter.WithLabelValues(req.Method).Inc()
			http.Error(w, fmt.Sprintf("Method not allowed: %s", req.Method), http.StatusMethodNotAllowed)
			return
		}
		var signedUrl string
		if signedReq, err := signReq(req.Method, req.PathValue("path")); err != nil {
			errorsCounter.WithLabelValues("signing").Inc()
			slog.Error(err.Error())
			http.Error(w, "Failed to sign URL", http.StatusInternalServerError)
			return
		} else {
			signedUrl = signedReq.URL
		}
		if req.Method == "PUT" && !slices.Contains(req.Header["Expect"], "100-continue") {
			if !proxyPuts {
				http.Error(w, "You sent a PUT request without the 'Expect: 100-continue' header, but this server does not have proxying of PUT requests enabled.", http.StatusBadRequest)
				return
			}
			proxyReq, err := http.NewRequest("PUT", signedUrl, &countingReader{ReadCloser: req.Body, counter: proxiedBytes})
			if err != nil {
				slog.Error(err.Error())
				http.Error(w, fmt.Sprintf("Failed to create proxy request for the signed URL '%s'", signedUrl), http.StatusInternalServerError)
				return
			}
			proxiedCounter.Inc()
			proxyRes, err := http.DefaultClient.Do(proxyReq)
			if err != nil {
				slog.Error(err.Error())
				http.Error(w, fmt.Sprintf("Failed to execute proxy request for the signed URL '%s'", signedUrl), http.StatusInternalServerError)
				return
			}
			defer proxyRes.Body.Close()
			for k, h := range proxyRes.Header {
				for _, v := range h {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(proxyRes.StatusCode)
			if _, err := io.Copy(w, proxyRes.Body); err != nil {
				slog.Error(err.Error())
			}
			return
		}
		http.Redirect(w, req, signedUrl, http.StatusTemporaryRedirect)
	}))

	server := http.Server{Addr: addr, Handler: serveMux}
	serveErr := make(chan error)
	go func() { serveErr <- server.ListenAndServe() }()
	slog.Info("Startup completed")
	probesReady.Store(true)
	probesHealthy.Store(true)
	select {
	case <-ctx.Done():
		return server.Shutdown(ctx)
	case err := <-serveErr:
		return err
	}
}

type countingReader struct {
	io.ReadCloser
	counter prometheus.Counter
}

func (w *countingReader) Read(p []byte) (int, error) {
	n, err := w.ReadCloser.Read(p)
	w.counter.Add(float64(n))
	return n, err
}

func (w *countingReader) Close() error {
	return w.ReadCloser.Close()
}
