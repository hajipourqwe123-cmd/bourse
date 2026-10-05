// Command tkarchive runs the localhost archive receiver (serve) or validates
// an existing archive (validate). The archive root must be outside the repo.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"bourse/research/tkarchive"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tkarchive serve|validate [-root DIR] [-port N]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	root := fs.String("root", `D:\Bourse\data\tablokhani_archive`, "archive root (outside git)")
	port := fs.Int("port", 8765, "port on 127.0.0.1")
	from := fs.String("from", "2026-01-04", "validation range start")
	to := fs.String("to", "2026-10-04", "validation range end")
	dir := fs.String("dir", `C:\Users\Mr.Hajipour\Downloads`, "download landing folder (ingest)")
	fs.Parse(os.Args[2:])
	st, err := tkarchive.NewStore(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "serve":
		ln, err := tkarchive.Listen(*port)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("listening on", ln.Addr())
		fmt.Fprintln(os.Stderr, http.Serve(ln, tkarchive.Handler(st)))
	case "ingest":
		rep, err := tkarchive.Ingest(st, *dir)
		if rep != nil {
			j, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(j))
			os.WriteFile(filepath.Join(*root, "manifests", "ingest_report.json"), j, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "validate":
		if err := tkarchive.WriteReport(st, *from, *to); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
