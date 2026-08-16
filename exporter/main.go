// Command queue-exporter sits in front of Ollama as a reverse proxy so it can
// count in-flight requests and expose that count as a Prometheus metric.
// Ollama itself has no /metrics endpoint, so this is the only way to get a
// real request-concurrency signal for KEDA to scale on.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

var inFlight int64

func main() {
	target, err := url.Parse("http://localhost:11434")
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&inFlight, 1)
			defer atomic.AddInt64(&inFlight, -1)
			proxy.ServeHTTP(w, r)
		})
		log.Fatal(http.ListenAndServe(":8081", mux))
	}()

	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintln(w, "# HELP ollama_request_queue_length In-flight requests currently proxied to Ollama.")
		fmt.Fprintln(w, "# TYPE ollama_request_queue_length gauge")
		fmt.Fprintf(w, "ollama_request_queue_length %d\n", atomic.LoadInt64(&inFlight))
	})
	log.Fatal(http.ListenAndServe(":9113", metricsMux))
}
