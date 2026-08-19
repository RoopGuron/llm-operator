// Command queue-exporter sits in front of Ollama as a reverse proxy so it can
// count in-flight requests and expose that count as a Prometheus metric.
// Ollama itself has no /metrics endpoint, so this is the only way to get a
// real request-concurrency signal for KEDA to scale on.
//
// It also scans /api/generate, /api/chat, /v1/chat/completions, and
// /v1/completions responses for the prompt/completion token counts Ollama
// reports on completion, so the platform dashboard can chart token
// generation velocity and estimated cost saved.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

var (
	inFlight              int64
	promptTokensTotal     int64
	completionTokensTotal int64
)

// tokenUsageFields covers both response shapes Ollama can return a finished
// completion in: its native /api/generate and /api/chat shape (done: true
// plus eval counts), and the OpenAI-compatible /v1/chat/completions and
// /v1/completions shape (a top-level or SSE-chunk "usage" object).
type tokenUsageFields struct {
	Done            bool  `json:"done"`
	PromptEvalCount int64 `json:"prompt_eval_count"`
	EvalCount       int64 `json:"eval_count"`
	Usage           *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

// tokenScanningBody wraps an Ollama response body and, as it is streamed
// through to the client unmodified, scans completed NDJSON lines for token
// counts so it can tally them without buffering the whole response (which
// would defeat streaming for chat clients).
type tokenScanningBody struct {
	io.ReadCloser
	buf []byte
}

func (t *tokenScanningBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		t.buf = append(t.buf, p[:n]...)
		for {
			i := bytes.IndexByte(t.buf, '\n')
			if i < 0 {
				break
			}
			t.scanLine(t.buf[:i])
			t.buf = t.buf[i+1:]
		}
	}
	if err == io.EOF && len(t.buf) > 0 {
		t.scanLine(t.buf)
		t.buf = nil
	}
	return n, err
}

func (t *tokenScanningBody) scanLine(line []byte) {
	// OpenAI-compatible streaming sends SSE frames ("data: {...}" / "data: [DONE]").
	line = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
	if len(line) == 0 {
		return
	}

	var fields tokenUsageFields
	if json.Unmarshal(line, &fields) != nil {
		return
	}
	switch {
	case fields.Done:
		atomic.AddInt64(&promptTokensTotal, fields.PromptEvalCount)
		atomic.AddInt64(&completionTokensTotal, fields.EvalCount)
	case fields.Usage != nil:
		atomic.AddInt64(&promptTokensTotal, fields.Usage.PromptTokens)
		atomic.AddInt64(&completionTokensTotal, fields.Usage.CompletionTokens)
	}
}

func main() {
	target, err := url.Parse("http://localhost:11434")
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		switch resp.Request.URL.Path {
		case "/api/generate", "/api/chat", "/v1/chat/completions", "/v1/completions":
			resp.Body = &tokenScanningBody{ReadCloser: resp.Body}
		}
		return nil
	}

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

		fmt.Fprintln(w, "# HELP ollama_prompt_tokens_total Cumulative prompt tokens evaluated across completed requests.")
		fmt.Fprintln(w, "# TYPE ollama_prompt_tokens_total counter")
		fmt.Fprintf(w, "ollama_prompt_tokens_total %d\n", atomic.LoadInt64(&promptTokensTotal))

		fmt.Fprintln(w, "# HELP ollama_completion_tokens_total Cumulative completion tokens generated across completed requests.")
		fmt.Fprintln(w, "# TYPE ollama_completion_tokens_total counter")
		fmt.Fprintf(w, "ollama_completion_tokens_total %d\n", atomic.LoadInt64(&completionTokensTotal))

		fmt.Fprintln(w, "# HELP ollama_tokens_generated_total Cumulative tokens generated across completed requests (alias of ollama_completion_tokens_total for TPS dashboards).")
		fmt.Fprintln(w, "# TYPE ollama_tokens_generated_total counter")
		fmt.Fprintf(w, "ollama_tokens_generated_total %d\n", atomic.LoadInt64(&completionTokensTotal))
	})
	log.Fatal(http.ListenAndServe(":9113", metricsMux))
}
