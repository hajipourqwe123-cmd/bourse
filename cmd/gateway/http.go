package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

// tokenTTL is the lifetime of a dev connection token; the client refreshes it.
const tokenTTL = 15 * time.Minute

// devOrigins may call the API cross-origin (the Next.js dev server).
var devOrigins = map[string]bool{"http://localhost:3000": true, "http://127.0.0.1:3000": true}

func serve(cfg Config, h *hub) (*http.Server, string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := h.state(time.Now()); !ok {
			http.Error(w, "rebuilding state", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/v1/time", func(w http.ResponseWriter, _ *http.Request) {
		// Server wall clock (unix ms) for the browser's clock-offset estimate (NTP-style, RTT/2).
		writeJSON(w, map[string]int64{"now": time.Now().UnixMilli()})
	})
	mux.HandleFunc("GET /api/v1/state", func(w http.ResponseWriter, _ *http.Request) {
		st, ok := h.state(time.Now())
		if !ok {
			http.Error(w, "rebuilding state", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, st)
	})
	mux.HandleFunc("GET /api/v1/token", func(w http.ResponseWriter, _ *http.Request) {
		// DEV ONLY (Sprint 7 brings real auth): anonymous tokens, only with GATEWAY_DEV_TOKEN=1 on a
		// loopback-bound gateway (checkLoopback refuses anything else at start).
		if !cfg.DevToken {
			http.NotFound(w, nil)
			return
		}
		if cfg.TokenSecret == "" {
			http.Error(w, "CENTRIFUGO_SECRET not set", http.StatusServiceUnavailable)
			return
		}
		now := time.Now()
		tok := devToken(cfg.TokenSecret, now)
		writeJSON(w, map[string]any{"token": tok, "exp": now.Add(tokenTTL).Unix()})
	})
	if st, err := os.Stat(cfg.WebDir); err == nil && st.IsDir() {
		mux.Handle("GET /", http.FileServer(http.Dir(cfg.WebDir)))
	} else {
		log.Printf("gateway: WEB_DIR %s not found; serving the API only (build the web app: cd web && npm run build)", cfg.WebDir)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); devOrigins[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
		}
		mux.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, "", err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway: http: %v", err)
		}
	}()
	return srv, ln.Addr().String(), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("gateway: write response: %v", err)
	}
}

// devToken is an anonymous Centrifugo connection JWT (HS256): sub "anon-<random>", 15-min exp.
func devToken(secret string, now time.Time) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"sub": "anon-" + hex.EncodeToString(b), "iat": now.Unix(), "exp": now.Add(tokenTTL).Unix()})
	unsigned := header + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}
