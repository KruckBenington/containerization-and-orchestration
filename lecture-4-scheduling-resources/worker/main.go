package main

import (
	"database/sql"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	var db *sql.DB
	var err error

	for {
		db, err = sql.Open("postgres", dbURL)
		if err == nil && db.Ping() == nil {
			log.Println("Worker connected to PostgreSQL")
			break
		}
		log.Println("Worker waiting for PostgreSQL...")
		time.Sleep(3 * time.Second)
	}

	for {
		time.Sleep(2 * time.Second)

		res, err := db.Exec(`
			UPDATE orders 
			SET status = 'processed' 
			WHERE id IN (
				SELECT id FROM orders 
				WHERE status = 'pending' 
				LIMIT 5 
				FOR UPDATE SKIP LOCKED
			)`)
		if err != nil {
			log.Printf("Worker query error: %v\n", err)
			continue
		}

		rowsAffected, _ := res.RowsAffected()
		if rowsAffected > 0 {
			log.Printf("Worker processed %d order(s)\n", rowsAffected)
		}
	}
}
