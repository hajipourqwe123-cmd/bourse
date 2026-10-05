package tkarchive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const AllowedOrigin = "https://tablokhani.com"

// Handler is a localhost-only receiver. It accepts archive payloads (never
// credentials) and writes them via Store. Request bodies are never logged.
func Handler(s *Store) http.Handler {
	logf, _ := os.OpenFile(filepath.Join(s.Root, "logs", "receiver-"+time.Now().UTC().Format("20060102")+".log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	audit := func(format string, a ...any) {
		if logf != nil {
			fmt.Fprintf(logf, time.Now().UTC().Format(time.RFC3339)+" "+format+"\n", a...)
		}
	}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("/pending", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		keys, err := s.Pending(q.Get("dataset"), q.Get("from"), q.Get("to"))
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		audit("pending dataset=%s from=%s to=%s n=%d", q.Get("dataset"), q.Get("from"), q.Get("to"), len(keys))
		reply(w, 200, map[string]any{"pending": keys})
	})
	mux.HandleFunc("/put", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			reply(w, 405, map[string]string{"error": "POST only"})
			return
		}
		var pr PutRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 128<<20)).Decode(&pr); err != nil {
			reply(w, 400, map[string]string{"error": "bad json"})
			return
		}
		e, err := s.Put(pr)
		switch {
		case errors.Is(err, ErrExists):
			audit("put %s %s exists", pr.Dataset, pr.Key)
			reply(w, 409, map[string]string{"error": err.Error()})
		case err != nil:
			audit("put %s %s rejected: %v", pr.Dataset, pr.Key, err)
			reply(w, 400, map[string]string{"error": err.Error()})
		default:
			audit("put %s %s ok records=%d bytes=%d sha=%s", pr.Dataset, pr.Key, e.RecordCount, e.ContentLength, e.SHA256[:12])
			reply(w, 200, e)
		}
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		var f struct {
			Dataset string `json:"dataset"`
			Key     string `json:"key"`
			Status  int    `json:"http_status"`
			Note    string `json:"note"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&f) != nil || !keyRe.MatchString(f.Key) || !validDatasets[f.Dataset] {
			reply(w, 400, map[string]string{"error": "bad request"})
			return
		}
		line, _ := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "dataset": f.Dataset, "key": f.Key, "http_status": f.Status, "note": f.Note})
		ff, err := os.OpenFile(filepath.Join(s.Root, "manifests", "failures.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			ff.Write(append(line, '\n'))
			ff.Close()
		}
		audit("fail %s %s status=%d", f.Dataset, f.Key, f.Status)
		reply(w, 200, map[string]bool{"ok": true})
	})
	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != AllowedOrigin {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", AllowedOrigin)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Listen binds strictly to 127.0.0.1.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}
