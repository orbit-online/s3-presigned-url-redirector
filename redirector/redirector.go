package redirector

import (
	"context"
	"fmt"
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

func Serve(ctx context.Context, addr string, allowedMethods []string, bucket string, ttl time.Duration) error {
	methodCounter := promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "s3_presigned_url_redirector",
		Name:      "requests_total",
		Help:      "Number of requests received",
	}, prometheus.UnconstrainedLabels{"method"})
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
		case "POST":
			// POST is a whole different animal. Skip it.
			errorsCounter.WithLabelValues("unsupported-method").Inc()
			return nil, fmt.Errorf("The POST method is explicitly not supported")
		default:
			errorsCounter.WithLabelValues("unsupported-method").Inc()
			return nil, fmt.Errorf("Unsupported method %s", method)
		}
	}

	serveMux := http.NewServeMux()
	serveMux.Handle("/{path...}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(allowedMethods, r.Method) {
			deniedCounter.WithLabelValues(r.Method).Inc()
			w.WriteHeader(http.StatusMethodNotAllowed)
			http.Error(w, fmt.Sprintf("Method not allowed: %s", r.Method), http.StatusMethodNotAllowed)
		} else {
			if signedReq, err := signReq(r.Method, r.PathValue("path")); err != nil {
				errorsCounter.WithLabelValues("signing").Inc()
				slog.Error(err.Error())
				http.Error(w, "Failed to sign URL", http.StatusInternalServerError)
			} else {
				http.Redirect(w, r, signedReq.URL, http.StatusTemporaryRedirect)
			}
		}
	}))

	server := http.Server{Addr: addr, Handler: serveMux}
	serveErr := make(chan error)
	go func() { serveErr <- server.ListenAndServe() }()
	slog.Info("Startup completed")
	probesReady = true
	probesHealthy = true
	select {
	case <-ctx.Done():
		return server.Shutdown(ctx)
	case err := <-serveErr:
		return err
	}
}
