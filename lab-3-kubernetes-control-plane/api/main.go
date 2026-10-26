// Shop API: health, create/list orders, Prometheus /metrics (Lab 3 Part 5).
// HEALTH_FAIL=true makes /health return 5xx (Lab 3 Part 2).
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	_ "github.com/lib/pq"
)

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests.",
	}, []string{"route", "method", "status"})
	httpErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_errors_total",
		Help: "Total HTTP 5xx responses.",
	}, []string{"route"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2, 5},
	}, []string{"route"})
)

func init() {
	prometheus.MustRegister(httpRequests, httpErrors, httpDuration)
}

type orderIn struct {
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

type orderOut struct {
	ID        int    `json:"id"`
	Item      string `json:"item"`
	Quantity  int    `json:"quantity"`
	Processed bool   `json:"processed"`
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

func tracked(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(status)).Inc()
		if status >= 500 {
			httpErrors.WithLabelValues(route).Inc()
		}
		httpDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
	}
}

func main() {
	dsn := env("DATABASE_URL", "postgres://shop:shop@postgres:5432/shop?sslmode=disable")
	addr := ":" + env("PORT", "8080")

	db, err := waitDB(dsn, 30, time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := initDB(db); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /health", tracked("/health", health))
	// /fail always 503 — for Part 5 alert demos (probes stay on /health)
	mux.HandleFunc("GET /fail", tracked("/fail", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"status":"fail"}`, http.StatusServiceUnavailable)
	}))
	mux.HandleFunc("POST /order", tracked("/order", createOrder(db)))
	mux.HandleFunc("GET /orders", tracked("/orders", listOrders(db)))

	log.Printf("api listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func health(w http.ResponseWriter, _ *http.Request) {
	v := strings.ToLower(os.Getenv("HEALTH_FAIL"))
	if v == "1" || v == "true" || v == "yes" {
		http.Error(w, `{"status":"fail"}`, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func createOrder(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in orderIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		in.Item = strings.TrimSpace(in.Item)
		if in.Item == "" {
			http.Error(w, `{"error":"item required"}`, http.StatusBadRequest)
			return
		}
		if in.Quantity < 1 {
			in.Quantity = 1
		}
		var out orderOut
		err := db.QueryRow(
			`INSERT INTO orders (item, quantity) VALUES ($1, $2)
			 RETURNING id, item, quantity, processed`,
			in.Item, in.Quantity,
		).Scan(&out.ID, &out.Item, &out.Quantity, &out.Processed)
		if err != nil {
			http.Error(w, `{"error":"db"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	}
}

func listOrders(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT id, item, quantity, processed FROM orders ORDER BY id`)
		if err != nil {
			http.Error(w, `{"error":"db"}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := make([]orderOut, 0)
		for rows.Next() {
			var o orderOut
			if err := rows.Scan(&o.ID, &o.Item, &o.Quantity, &o.Processed); err != nil {
				http.Error(w, `{"error":"db"}`, http.StatusInternalServerError)
				return
			}
			out = append(out, o)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS orders (
			id         SERIAL PRIMARY KEY,
			item       TEXT NOT NULL,
			quantity   INT  NOT NULL,
			processed  BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	return err
}

func waitDB(dsn string, retries int, delay time.Duration) (*sql.DB, error) {
	var last error
	for i := 0; i < retries; i++ {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			last = err
			time.Sleep(delay)
			continue
		}
		if err := db.Ping(); err != nil {
			last = err
			db.Close()
			time.Sleep(delay)
			continue
		}
		return db, nil
	}
	return nil, last
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
