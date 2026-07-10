// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package agent

import (
	"archive/tar"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleImport unpacks a tar request body into ?dir= (default workdir). Entries
// are constrained under the workdir; any path escaping it is skipped.
func (a *Agent) handleImport(w http.ResponseWriter, r *http.Request) {
	dir, err := a.resolve(r.URL.Query().Get("dir"))
	if err != nil {
		http.Error(w, "bad dir", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tr := tar.NewReader(r.Body)
	for {
		hdr, terr := tr.Next()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			http.Error(w, terr.Error(), http.StatusBadRequest)
			return
		}
		// Constrain the entry under dir (defense against tar path traversal).
		target := filepath.Join(dir, filepath.Clean("/"+hdr.Name))
		if target != dir && !strings.HasPrefix(target, dir+string(os.PathSeparator)) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			f, ferr := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if ferr != nil {
				http.Error(w, ferr.Error(), http.StatusInternalServerError)
				return
			}
			if _, cerr := io.Copy(f, tr); cerr != nil {
				_ = f.Close()
				http.Error(w, cerr.Error(), http.StatusInternalServerError)
				return
			}
			_ = f.Close()
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleExport streams ?dir= (default workdir) back as a tar of regular files.
func (a *Agent) handleExport(w http.ResponseWriter, r *http.Request) {
	dir, err := a.resolve(r.URL.Query().Get("dir"))
	if err != nil {
		http.Error(w, "bad dir", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	tw := tar.NewWriter(w)
	defer tw.Close()

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		hdr := &tar.Header{Name: rel, Mode: 0o644, Size: info.Size()}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer f.Close()
		_, cerr := io.Copy(tw, f)
		return cerr
	})
}
