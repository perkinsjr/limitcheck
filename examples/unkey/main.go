// Unkey-backed local target for limitcheck.
//
// Each request calls Unkey's ratelimit.limit API, then returns an HTTP
// response shaped the way limitcheck expects (429 + Retry-After +
// X-RateLimit-* + JSON reset body).
//
// Required env: UNKEY_ROOT_KEY
// Optional: UNKEY_NAMESPACE (default limitcheck.examples),
// UNKEY_LIMIT (default 5), UNKEY_WINDOW_MS (default 2000), ADDR (default 127.0.0.1:0)
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/perkinsjr/limitcheck/internal/hdr"
)

func main() {
	root := os.Getenv("UNKEY_ROOT_KEY")
	if root == "" {
		fmt.Fprintln(os.Stderr, "examples/unkey: set UNKEY_ROOT_KEY to a Unkey root key with ratelimit.*.limit")
		os.Exit(2)
	}
	ns := envOr("UNKEY_NAMESPACE", "limitcheck.examples")
	limit, err := strconv.Atoi(envOr("UNKEY_LIMIT", "5"))
	if err != nil || limit < 1 {
		fmt.Fprintln(os.Stderr, "examples/unkey: UNKEY_LIMIT must be a positive integer")
		os.Exit(2)
	}
	windowMs, err := strconv.Atoi(envOr("UNKEY_WINDOW_MS", "2000"))
	if err != nil || windowMs < 400 {
		fmt.Fprintln(os.Stderr, "examples/unkey: UNKEY_WINDOW_MS must be an integer >= 400")
		os.Exit(2)
	}
	addr := envOr("ADDR", "127.0.0.1:0")
	window := time.Duration(windowMs) * time.Millisecond

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "examples/unkey: listen: %v\n", err)
		os.Exit(2)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, client, root, ns, limit, windowMs, window)
	})
	fmt.Printf("http://%s/\n", ln.Addr().String())
	_ = http.Serve(ln, mux)
}

func serve(w http.ResponseWriter, r *http.Request, client *http.Client, root, ns string, limit, windowMs int, window time.Duration) {
	now := time.Now()
	id := r.Header.Get("X-Identity")
	if id == "" {
		id = "anon"
	}
	ok, rem, reset, err := unkeyLimit(client, root, ns, id, limit, windowMs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if reset.IsZero() {
		reset = now.Add(window)
	}
	hdr.Write(w, hdr.ShapeFull, hdr.Decision{
		Allowed: ok, Limit: limit, Remaining: rem, Reset: reset, Window: window, Now: now,
	})
}

type unkeyReq struct {
	Namespace  string `json:"namespace"`
	Identifier string `json:"identifier"`
	Limit      int    `json:"limit"`
	Duration   int    `json:"duration"`
}

type unkeyResp struct {
	Data struct {
		Success   bool  `json:"success"`
		Limit     int   `json:"limit"`
		Remaining int   `json:"remaining"`
		Reset     int64 `json:"reset"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func unkeyLimit(client *http.Client, root, ns, id string, limit, windowMs int) (bool, int, time.Time, error) {
	body, err := json.Marshal(unkeyReq{
		Namespace: ns, Identifier: id, Limit: limit, Duration: windowMs,
	})
	if err != nil {
		return false, 0, time.Time{}, err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.unkey.com/v2/ratelimit.limit", bytes.NewReader(body))
	if err != nil {
		return false, 0, time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+root)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return false, 0, time.Time{}, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return false, 0, time.Time{}, err
	}
	var parsed unkeyResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false, 0, time.Time{}, fmt.Errorf("unkey decode: %w (body %s)", err, truncate(raw))
	}
	if res.StatusCode >= 400 {
		msg := string(raw)
		if parsed.Error != nil {
			msg = parsed.Error.Message
		}
		return false, 0, time.Time{}, fmt.Errorf("unkey HTTP %d: %s", res.StatusCode, msg)
	}
	var reset time.Time
	if parsed.Data.Reset > 0 {
		reset = time.UnixMilli(parsed.Data.Reset)
	}
	return parsed.Data.Success, parsed.Data.Remaining, reset, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func truncate(b []byte) string {
	const n = 200
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
