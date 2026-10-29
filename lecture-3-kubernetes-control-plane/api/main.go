package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
)

type Order struct {
	ID        int       `json:"id"`
	Item      string    `json:"item"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

var db *sql.DB

func initDB() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Println("DATABASE_URL is not set, DB features disabled")
		return
	}

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		log.Printf("DB open error: %v\n", err)
		return
	}

	query := `
	CREATE TABLE IF NOT EXISTS orders (
		id SERIAL PRIMARY KEY,
		item TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	for {
		_, err = db.Exec(query)
		if err == nil {
			log.Println("Connected to PostgreSQL and verified schema")
			return
		}
		log.Printf("Waiting for PostgreSQL: %v\n", err)
		time.Sleep(3 * time.Second)
	}
}

func main() {
	go initDB()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("HEALTH_FAIL") == "true" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("unhealthy\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	http.HandleFunc("/order", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if db == nil {
			http.Error(w, "Database not connected", http.StatusServiceUnavailable)
			return
		}

		var req struct {
			Item string `json:"item"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Item == "" {
			req.Item = "default-item"
		}

		var id int
		err := db.QueryRow("INSERT INTO orders (item, status) VALUES ($1, 'pending') RETURNING id", req.Item).Scan(&id)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to insert order: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "Order %d created\n", id)
	})

	http.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			http.Error(w, "Database not connected", http.StatusServiceUnavailable)
			return
		}

		rows, err := db.Query("SELECT id, item, status, created_at FROM orders ORDER BY id DESC LIMIT 50")
		if err != nil {
			http.Error(w, fmt.Sprintf("Query error: %v", err), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var orders []Order
		for rows.Next() {
			var o Order
			if err := rows.Scan(&o.ID, &o.Item, &o.Status, &o.CreatedAt); err != nil {
				continue
			}
			orders = append(orders, o)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orders)
	})

	log.Println("API listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
