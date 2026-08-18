package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rhw/m365backup/internal/blobstore"
	"github.com/rhw/m365backup/internal/catalog"
	"github.com/rhw/m365backup/internal/storage"
)

func main() {
	root := flag.String("root", "", "tenant store root (blobs/ + manifests/)")
	password := flag.String("password", "", "store password")
	service := flag.String("service", "exchange", "service (exchange|onedrive|teams|sharepoint)")
	generation := flag.Int("generation", 0, "manifest generation (required)")
	out := flag.String("out", "", "output directory")
	flag.Parse()
	if *root == "" || *password == "" || *out == "" || *generation < 1 {
		fmt.Fprintf(os.Stderr, "usage: m365-restore --root PATH --password PASS --service exchange --generation N --out DIR\n")
		os.Exit(2)
	}
	if _, err := storage.GuardPath(*root); err != nil {
		fatal(err)
	}
	if _, err := storage.GuardPath(*out); err != nil {
		fatal(err)
	}
	blobs, err := blobstore.New(*root, *password)
	if err != nil {
		fatal(err)
	}
	st := &catalog.Store{Root: *root, Password: *password, Blobs: blobs}
	man, err := st.ReadManifest(*service, *generation)
	if err != nil {
		fatal(fmt.Errorf("manifest: %w", err))
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fatal(err)
	}
	n := 0
	for _, e := range man.Items {
		if e.Path == "" || e.Hash == "" {
			continue
		}
		data, err := blobs.Get(e.Hash)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", e.Path, err))
		}
		dest, err := storage.EnsureSubpath(*out, e.Path)
		if err != nil {
			fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			fatal(err)
		}
		n++
	}
	fmt.Printf("restored %d files from %s generation %d → %s\n", n, *service, *generation, *out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
