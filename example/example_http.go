package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Ishimwe-Kevin/devlite-app/devlite-go"
)

// example_http.go is the DevLite Go SDK hello-world: instrument a stdlib
// server with two lines, then let DevLite capture requests, panics, and
// manual errors. Run:
//
//	DEVLITE_API_KEY=dl_... go run ./example
//
// then hit http://localhost:8080/ and http://localhost:8080/boom.

func main() {
	if err := devlite.Init(
		devlite.WithAPIKey(envOr("DEVLITE_API_KEY", "dl_...")),
		devlite.WithServiceName("go-example"),
		devlite.WithEnvironment("development"),
	); err != nil {
		log.Fatal(err)
	}
	defer devlite.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		devlite.AddBreadcrumb(map[string]any{"type": "info", "message": "serving home"})
		devlite.SetUser(map[string]any{"id": "u-1", "email": "kevin@example.com"})
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("you asked for coffee\n"))
	})

	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("intentional panic")
	})

	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		devlite.CaptureErrorCtx(r.Context(), errors.New("card declined"), map[string]any{"order": "o-9"})
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/metric", func(w http.ResponseWriter, r *http.Request) {
		devlite.ReportMetric("example.counter", float64(time.Now().UnixMilli()%1000), "count", map[string]string{"route": "/metric"})
		w.WriteHeader(http.StatusOK)
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", devlite.Middleware(mux)))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
