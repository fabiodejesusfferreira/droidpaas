package api

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// handleBackup entrega uma cópia consistente do SQLite (VACUUM INTO). Com ?key=1 vem um .tar.gz com a master.key
// (sensível: quem tem o banco e a chave lê os segredos). A ação é auditada (TSD §14.3).
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	withKey := r.URL.Query().Get("key") == "1"
	tmpDir := s.Cfg.Dir("tmp")
	os.MkdirAll(tmpDir, 0o700)
	tmp := filepath.Join(tmpDir, "backup-"+time.Now().UTC().Format("20060102T150405Z")+".db")
	defer os.Remove(tmp)
	if _, err := s.Store.DB.ExecContext(r.Context(), "vacuum into ?", tmp); err != nil {
		s.internalErr(w, err)
		return
	}
	os.Chmod(tmp, 0o600)
	s.Events.Info(r.Context(), "backup.created", nil, "backup do banco gerado", map[string]any{"com_chave": withKey})
	stamp := time.Now().UTC().Format("20060102-150405")
	if !withKey {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="droidpaas-`+stamp+`.db"`)
		f, err := os.Open(tmp)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		defer f.Close()
		io.Copy(w, f)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="droidpaas-`+stamp+`.tar.gz"`)
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	add := func(name, path string) error {
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime()}); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}
	if err := add("droidpaas.db", tmp); err != nil {
		return
	}
	add("master.key", s.Cfg.KeyPath())
}
