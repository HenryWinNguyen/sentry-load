// Command guineapig is the M1-M4 load-test target: a small HTTP app with
// a fast endpoint and a deliberately bottlenecked one, so load-test
// results show a real breaking point instead of a flat line. See SCOPE.md
// "guinea-pig app design" in the decisions log for why.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	_ "modernc.org/sqlite"
)

const dbPath = "guineapig.db"

// simulatedQueryLatency stands in for the network round-trip to a real,
// remote database. Local SQLite executes a query in well under a
// millisecond, which made the first version of this app's /slow endpoint
// just as fast as /fast — the N+1 pattern and tiny pool never actually
// contended for anything. Real N+1 bugs hurt because each query pays a
// real network round trip (a few ms, easily), not because the query
// itself is slow — this makes that cost real without needing an actual
// networked database for a throwaway demo app.
const simulatedQueryLatency = 4 * time.Millisecond

type Product struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	ReviewCount int      `json:"review_count,omitempty"`
	Reviews     []string `json:"reviews,omitempty"`
}

func main() {
	fastDB := mustOpenDB(20)
	slowDB := mustOpenDB(2) // deliberately tiny pool, see SCOPE.md
	seed(fastDB)

	mux := http.NewServeMux()
	mux.HandleFunc("/fast", fastHandler(fastDB))
	mux.HandleFunc("/slow", slowHandler(slowDB))

	addr := ":8081"
	log.Printf("guinea-pig app listening on %s (/fast = single JOIN, /slow = intentional N+1 + 2-connection pool)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func mustOpenDB(maxOpenConns int) *sql.DB {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	return db
}

func seed(db *sql.DB) {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS reviews`,
		`DROP TABLE IF EXISTS products`,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE reviews (id INTEGER PRIMARY KEY, product_id INTEGER, body TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			log.Fatalf("seed schema: %v", err)
		}
	}
	for i := 1; i <= 20; i++ {
		if _, err := db.Exec(`INSERT INTO products (id, name) VALUES (?, ?)`, i, fmt.Sprintf("Product %d", i)); err != nil {
			log.Fatalf("seed product: %v", err)
		}
		for r := 1; r <= 5; r++ {
			body := fmt.Sprintf("Review %d for product %d", r, i)
			if _, err := db.Exec(`INSERT INTO reviews (product_id, body) VALUES (?, ?)`, i, body); err != nil {
				log.Fatalf("seed review: %v", err)
			}
		}
	}
}

// fastHandler returns products with review counts via a single JOIN query.
func fastHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(simulatedQueryLatency) // one query, one round trip
		rows, err := db.QueryContext(r.Context(), `
			SELECT p.id, p.name, COUNT(rv.id)
			FROM products p
			LEFT JOIN reviews rv ON rv.product_id = p.id
			GROUP BY p.id, p.name
			ORDER BY p.id`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var products []Product
		for rows.Next() {
			var p Product
			if err := rows.Scan(&p.ID, &p.Name, &p.ReviewCount); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			products = append(products, p)
		}
		writeJSON(w, products)
	}
}

// slowHandler returns the same logical data the deliberately wrong way:
// one query for products, then one extra query PER product for its
// reviews (classic N+1), against a pool capped at 2 connections. Under
// concurrent load, requests queue for those 2 connections and latency
// climbs sharply, while /fast (one query, a 20-connection pool) stays
// flat — that contrast is the whole point of this app.
func slowHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		time.Sleep(simulatedQueryLatency) // query 1 of N+1
		rows, err := db.QueryContext(ctx, `SELECT id, name FROM products ORDER BY id`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var products []Product
		for rows.Next() {
			var p Product
			if err := rows.Scan(&p.ID, &p.Name); err != nil {
				rows.Close()
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			products = append(products, p)
		}
		rows.Close()

		for i, p := range products {
			time.Sleep(simulatedQueryLatency) // one extra round trip per product — the "+N"
			reviewRows, err := db.QueryContext(ctx, `SELECT body FROM reviews WHERE product_id = ?`, p.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			var reviews []string
			for reviewRows.Next() {
				var body string
				if err := reviewRows.Scan(&body); err != nil {
					reviewRows.Close()
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				reviews = append(reviews, body)
			}
			reviewRows.Close()
			products[i].Reviews = reviews
		}
		writeJSON(w, products)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
