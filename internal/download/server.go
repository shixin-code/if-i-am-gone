package download

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Server struct {
	store Store
	now   func() time.Time
}

func NewServer(store Store) *Server {
	return &Server{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func NewServerForTest(store Store, now func() time.Time) *Server {
	s := NewServer(store)
	if now != nil {
		s.now = now
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/download/", s.handleDownload)
	return mux
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/download/")
	if token == "" || strings.Contains(token, "/") {
		s.reject(w, http.StatusNotFound, "download_token_invalid", token, r)
		return
	}

	dt, err := s.store.GetDownloadToken(token)
	if err != nil {
		s.reject(w, http.StatusInternalServerError, "download_token_lookup_failed", err.Error(), r)
		return
	}
	if dt == nil {
		s.reject(w, http.StatusNotFound, "download_token_missing", token, r)
		return
	}
	now := s.now()
	if !now.Before(dt.ExpiresAt) {
		s.reject(w, http.StatusGone, "download_token_expired", dt.Beneficiary, r)
		return
	}
	if dt.DownloadCount >= dt.MaxDownloads {
		s.reject(w, http.StatusGone, "download_token_limit_exceeded", dt.Beneficiary, r)
		return
	}
	info, err := os.Stat(dt.ArchivePath)
	if err != nil {
		s.reject(w, http.StatusNotFound, "download_archive_missing", dt.Beneficiary, r)
		return
	}
	if info.IsDir() {
		s.reject(w, http.StatusNotFound, "download_archive_is_dir", dt.Beneficiary, r)
		return
	}
	if err := s.store.IncrementDownloadCount(token); err != nil {
		s.reject(w, http.StatusInternalServerError, "download_count_increment_failed", err.Error(), r)
		return
	}
	_ = s.store.Audit("download_success", dt.Beneficiary, now)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(dt.ArchivePath)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, dt.ArchivePath)
}

func (s *Server) reject(w http.ResponseWriter, status int, event, detail string, r *http.Request) {
	_ = s.store.Audit(event, detail, s.now())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(status), status)
}
