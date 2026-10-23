package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var (
	requests    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_requests_total", Help: "Total HTTP requests."}, []string{"route", "method", "status"})
	errorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_errors_total", Help: "Total HTTP 5xx responses."}, []string{"route"})
	duration    = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration in seconds.", Buckets: []float64{.01, .05, .1, .25, .5, 1, 2, 3, 5, 10}}, []string{"route"})
)

func logJSON(ctx context.Context, level, message string, fields map[string]any) {
	sc := trace.SpanContextFromContext(ctx)
	entry := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "message": message, "service": "api"}
	if sc.IsValid() {
		entry["trace_id"] = sc.TraceID().String()
		entry["span_id"] = sc.SpanID().String()
	}
	for k, v := range fields {
		entry[k] = v
	}
	b, err := json.Marshal(entry)
	if err != nil {
		log.Printf("marshal log: %v", err)
		return
	}
	fmt.Println(string(b))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func tracked(route string, fn http.HandlerFunc) http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			elapsed := time.Since(start)
			requests.WithLabelValues(route, r.Method, strconv.Itoa(status)).Inc()
			if status >= 500 {
				errorsTotal.WithLabelValues(route).Inc()
			}
			duration.WithLabelValues(route).Observe(elapsed.Seconds())
			level := "info"
			if status >= 500 {
				level = "error"
			}
			logJSON(r.Context(), level, "request completed", map[string]any{"route": route, "method": r.Method, "status": status, "duration_ms": elapsed.Milliseconds()})
		}()
		fn(sw, r)
	})
	return otelhttp.NewHandler(handler, route)
}

func initTracing(ctx context.Context) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:4317"
	}
	var opts []otlptracegrpc.Option
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		opts = append(opts, otlptracegrpc.WithEndpoint(strings.TrimPrefix(endpoint, "https://")))
	case strings.HasPrefix(endpoint, "http://"):
		opts = append(opts, otlptracegrpc.WithEndpoint(strings.TrimPrefix(endpoint, "http://")), otlptracegrpc.WithInsecure())
	default:
		opts = append(opts, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", "api")))
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}

func main() {
	prometheus.MustRegister(requests, errorsTotal, duration)
	shutdown, err := initTracing(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			log.Printf("tracing shutdown: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/health", tracked("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	mux.Handle("/fail", tracked("/fail", func(w http.ResponseWriter, r *http.Request) {
		err := errors.New("simulated failure")
		span := trace.SpanFromContext(r.Context())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}))
	mux.Handle("/slow", tracked("/slow", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("api").Start(r.Context(), "slow-op")
		defer span.End()
		delay := time.Duration(1+rand.Intn(3)) * time.Second
		select {
		case <-time.After(delay):
			logJSON(ctx, "info", "slow operation completed", map[string]any{"delay_ms": delay.Milliseconds()})
			_, _ = w.Write([]byte("slow done"))
		case <-ctx.Done():
			span.RecordError(ctx.Err())
			span.SetStatus(codes.Error, ctx.Err().Error())
		}
	}))
	mux.Handle("/load", tracked("/load", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("count"))
		if n <= 0 {
			n = 30
		}
		if n > 500 {
			n = 500
		}
		client := &http.Client{Timeout: 5 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)}
		var wg sync.WaitGroup
		var failed int
		var mu sync.Mutex
		limit := make(chan struct{}, 20)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case limit <- struct{}{}:
					defer func() { <-limit }()
				case <-r.Context().Done():
					mu.Lock()
					failed++
					mu.Unlock()
					return
				}
				req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://127.0.0.1:8080/health", nil)
				if err == nil {
					var resp *http.Response
					resp, err = client.Do(req)
					if err == nil {
						_ = resp.Body.Close()
						if resp.StatusCode >= 400 {
							err = fmt.Errorf("unexpected status %d", resp.StatusCode)
						}
					}
				}
				if err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if failed > 0 {
			w.WriteHeader(http.StatusBadGateway)
		}
		fmt.Fprintf(w, "generated %d requests, failed %d\n", n, failed)
	}))
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Println("api listening on :8080")
	log.Fatal(srv.ListenAndServe())
}
