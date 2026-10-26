// Shop worker: marks unprocessed orders as processed.
package main

import (
	"database/sql"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	dsn := env("DATABASE_URL", "postgres://shop:shop@postgres:5432/shop?sslmode=disable")
	poll := envDuration("POLL_SECONDS", 2*time.Second)

	db, err := waitDB(dsn, 30, time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	log.Println("worker started")
	for {
		n, err := processOnce(db)
		if err != nil {
			log.Printf("worker error: %v", err)
		} else if n > 0 {
			log.Printf("processed %d order(s)", n)
		}
		time.Sleep(poll)
	}
}

func processOnce(db *sql.DB) (int64, error) {
	res, err := db.Exec(`
		UPDATE orders
		   SET processed = TRUE
		 WHERE id IN (
			SELECT id FROM orders
			 WHERE processed = FALSE
			 ORDER BY id
			 LIMIT 10
			 FOR UPDATE SKIP LOCKED
		 )`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	sec, err := strconv.ParseFloat(v, 64)
	if err != nil || sec <= 0 {
		return def
	}
	return time.Duration(sec * float64(time.Second))
}
