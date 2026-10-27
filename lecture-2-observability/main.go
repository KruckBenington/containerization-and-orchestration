package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tracer trace.Tracer

	httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "http_requests_total", Help: "Все входящие запросы"},
		[]string{"path", "code"},
	)
	httpErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "http_errors_total", Help: "Ошибки 5xx"},
		[]string{"path"},
	)
	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Время ответа в секундах",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 3.5},
		},
		[]string{"path"},
	)
)

func init() {
	prometheus.MustRegister(httpRequests, httpErrors, httpDuration)
}

func writeJSONLog(level, msg, path, traceID string, status int) {
	logEntry := map[string]interface{}{
		"time":     time.Now().Format(time.RFC3339),
		"level":    level,
		"msg":      msg,
		"path":     path,
		"status":   status,
		"trace_id": traceID,
	}
	bytes, _ := json.Marshal(logEntry)
	os.Stdout.Write(append(bytes, '\n'))
}

func initTracer() {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "jaeger:4317"
	}
	ctx := context.Background()
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		fmt.Printf("Tracer init err: %v\n", err)
		return
	}
	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String("api")))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("api")
}

func main() {
	initTracer()

	// 1. GET /health
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "/health")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		httpRequests.WithLabelValues("/health", "200").Inc()
		httpDuration.WithLabelValues("/health").Observe(0.001)
		writeJSONLog("info", "health check ok", "/health", traceID, 200)

		w.Write([]byte("ok\n"))
	})

	// 2. GET /fail
	http.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "/fail")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		span.SetStatus(codes.Error, "fail endpoint triggered")

		httpRequests.WithLabelValues("/fail", "500").Inc()
		httpErrors.WithLabelValues("/fail").Inc()
		httpDuration.WithLabelValues("/fail").Observe(0.005)
		writeJSONLog("error", "internal server failure triggered", "/fail", traceID, 500)

		http.Error(w, "internal server error", http.StatusInternalServerError)
	})

	// 3. GET /slow
	http.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "/slow")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		start := time.Now()

		_, childSpan := tracer.Start(ctx, "slow-op")
		sleepDur := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
		time.Sleep(sleepDur)
		childSpan.End()

		duration := time.Since(start).Seconds()
		httpRequests.WithLabelValues("/slow", "200").Inc()
		httpDuration.WithLabelValues("/slow").Observe(duration)
		writeJSONLog("info", "slow operation finished", "/slow", traceID, 200)

		fmt.Fprintf(w, "slept for %v\n", sleepDur)
	})

	// 4. GET /load
	http.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "/load")
		defer span.End()
		traceID := span.SpanContext().TraceID().String()

		for i := 0; i < 30; i++ {
			go func() {
				res, err := http.Get("http://127.0.0.1:8080/health")
				if err == nil {
					_ = res.Body.Close()
				}
			}()
		}

		httpRequests.WithLabelValues("/load", "200").Inc()
		httpDuration.WithLabelValues("/load").Observe(0.01)
		writeJSONLog("info", "load generated 30 requests", "/load", traceID, 200)

		w.Write([]byte("load triggered: 30 requests\n"))
	})

	// 5. GET /metrics
	http.Handle("/metrics", promhttp.Handler())

	fmt.Println("API service listening on :8080...")
	_ = http.ListenAndServe(":8080", nil)
}
