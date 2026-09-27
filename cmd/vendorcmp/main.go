// vendorcmp compares the two market-data vendors (D-03, docs/vendor-comparison.md). LOCAL ONLY:
// it fetches each vendor's all-symbols payload once (or reads saved payloads), joins the rows by
// TSETMC instrument code (falling back to the normalised symbol), and writes a Persian markdown
// report: per-field coverage, exact-match rate, mismatch examples, value formats and row counts
// per class. The report holds statistics and a few example values, never secrets or raw dumps.
//
//	BRSAPI_KEY=… SOURCEARENA_TOKEN=… go run ./cmd/vendorcmp -out docs/vendor-comparison.md
//	go run ./cmd/vendorcmp -brsapi saved.json -sourcearena saved.json -out report.md
//
// Compare payloads of the same moment: with the market open the vendors lag each other by an
// unmeasured amount (pre-open recording of 2026-09-26), so closed-market payloads give the fair
// comparison.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	brsFile := flag.String("brsapi", "", "saved BrsApi AllSymbols payload (default: fetch with BRSAPI_KEY)")
	saFile := flag.String("sourcearena", "", "saved SourceArena all-symbols payload (default: fetch with SOURCEARENA_TOKEN)")
	out := flag.String("out", "docs/vendor-comparison.md", "markdown report path")
	note := flag.String("note", "", "context shown under the header (e.g. which trading day the payloads hold)")
	flag.Parse()

	brs, brsAt, err := load(*brsFile, "BRSAPI_KEY", brsURL)
	if err != nil {
		log.Fatalf("vendorcmp: brsapi: %v", err)
	}
	sa, saAt, err := load(*saFile, "SOURCEARENA_TOKEN", saURL)
	if err != nil {
		log.Fatalf("vendorcmp: sourcearena: %v", err)
	}
	a, err := parseRows(brs)
	if err != nil {
		log.Fatalf("vendorcmp: brsapi payload: %v", err)
	}
	b, err := parseRows(sa)
	if err != nil {
		log.Fatalf("vendorcmp: sourcearena payload: %v", err)
	}
	rep := compare(a, b)
	rep.BrsAt, rep.SaAt, rep.Note = brsAt, saAt, *note
	if err := os.WriteFile(*out, []byte(rep.Markdown()), 0o644); err != nil {
		log.Fatalf("vendorcmp: %v", err)
	}
	log.Printf("vendorcmp: %d BrsApi rows, %d SourceArena rows, %d joined -> %s", len(a), len(b), len(rep.Pairs), *out)
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"

func brsURL(key string) string {
	return "https://api.brsapi.ir/Tsetmc/AllSymbols.php?" + url.Values{"type": {"1"}, "key": {key}}.Encode()
}

func saURL(token string) string {
	return "https://apis.sourcearena.ir/api/?token=" + url.QueryEscape(token) + "&all&type=0"
}

// load reads a saved payload, or fetches one with the secret from env var secretVar. It returns
// the payload and a description of its origin and time (no secret).
func load(file, secretVar string, mkURL func(string) string) ([]byte, string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, "", err
		}
		at := "فایل ذخیره‌شده" // the file time is not the data time: see the latest time inside the payload
		if st, err := os.Stat(file); err == nil {
			at += "، زمان فایل " + st.ModTime().UTC().Format("2006-01-02 15:04 UTC")
		}
		return b, at, nil
	}
	secret := os.Getenv(secretVar)
	if secret == "" {
		return nil, "", fmt.Errorf("%s is not set (or pass a saved payload)", secretVar)
	}
	body, err := fetch(mkURL(secret), secret)
	return body, "دریافت زنده، " + time.Now().UTC().Format("2006-01-02 15:04 UTC"), err
}

// fetch performs one GET without following redirects; errors never carry the URL or secret.
func fetch(u, secret string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("build request failed")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, */*")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = fmt.Errorf("%s: %w", ue.Op, ue.Err)
		}
		return nil, errors.New(scrub(err.Error(), secret))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, errors.New(scrub(err.Error(), secret))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func scrub(msg, secret string) string {
	for _, s := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return msg
}
