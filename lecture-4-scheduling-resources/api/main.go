package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Order struct {
	ID        int       `json:"id"`
	Item      string    `json:"item"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	db *sql.DB

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests",
		},
		[]string{"path", "code"},
	)

	errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_errors_total",
			Help: "Total HTTP 5xx errors",
		},
		[]string{"path"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2},
		},
		[]string{"path"},
	)
)

func init() {
	prometheus.MustRegister(requestsTotal, errorsTotal, requestDuration)
}

type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func promMiddleware(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next(sw, r)

		duration := time.Since(start).Seconds()
		requestDuration.WithLabelValues(path).Observe(duration)
		requestsTotal.WithLabelValues(path, strconv.Itoa(sw.statusCode)).Inc()

		if sw.statusCode >= 500 {
			errorsTotal.WithLabelValues(path).Inc()
		}
	}
}

func initDB() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return
	}
	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		return
	}
	query := `CREATE TABLE IF NOT EXISTS orders (id SERIAL PRIMARY KEY, item TEXT NOT NULL, status TEXT NOT NULL, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);`
	for {
		if _, err = db.Exec(query); err == nil {
			return
		}
		time.Sleep(3 * time.Second)
	}
}

func main() {
	go initDB()

	http.HandleFunc("/health", promMiddleware("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	}))

	http.HandleFunc("/fail", promMiddleware("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error\n"))
	}))

	http.HandleFunc("/slow", promMiddleware("/slow", func(w http.ResponseWriter, r *http.Request) {
		delay := time.Duration(300+rand.Intn(700)) * time.Millisecond
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "slept %v\n", delay)
	}))

	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/order", promMiddleware("/order", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if db == nil {
			http.Error(w, "DB not ready", http.StatusServiceUnavailable)
			return
		}
		var req struct { Item string `json:"item"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Item == "" { req.Item = "item" }
		var id int
		_ = db.QueryRow("INSERT INTO orders (item, status) VALUES ($1, 'pending') RETURNING id", req.Item).Scan(&id)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "Order %d\n", id)
	}))

	http.HandleFunc("/orders", promMiddleware("/orders", func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			http.Error(w, "DB not ready", http.StatusServiceUnavailable)
			return
		}
		rows, err := db.Query("SELECT id, item, status, created_at FROM orders ORDER BY id DESC LIMIT 10")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		var orders []Order
		for rows.Next() {
			var o Order
			if err := rows.Scan(&o.ID, &o.Item, &o.Status, &o.CreatedAt); err == nil {
				orders = append(orders, o)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orders)
	}))

	log.Println("API listening on :8080...")
	_ = http.ListenAndServe(":8080", nil)
}
