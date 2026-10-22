package admin

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/backup"
)

// A backup holds the whole site, and a restore replaces it, so all of this is for admins only.
func (h *Handler) registerBackups(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/backup", admin(h.backupPage))
	mux.Handle("POST /admin/backup/download", admin(h.backupDownload))
	mux.Handle("POST /admin/backup/save", admin(h.backupSave))
	mux.Handle("GET /admin/backup/files/{name}", admin(h.backupFile))
	mux.Handle("POST /admin/backup/files/{name}/delete", admin(h.backupDelete))
	mux.Handle("POST /admin/backup/files/{name}/review", admin(h.backupReviewFile))
	// the upload may be large: its size is capped before anything reads it
	mux.Handle("POST /admin/backup/upload", limitBody(backup.MaxArchiveBytes+1<<20, admin(h.backupUpload)))
	mux.Handle("POST /admin/backup/restore", admin(h.backupRestore))
}

// restoreWord must be typed to restore: a replace-everything action shouldn't be one click away.
const restoreWord = "RESTORE"

type backupScreen struct {
	Files     []backup.File
	KeepAuto  int
	Personal  bool // scheduled backups include subscribers and messages
	LastAuto  *time.Time
	Folder    bool
	RestoreIf string
}

func (h *Handler) renderBackup(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string) {
	files, err := h.Backup.List()
	if err != nil {
		serverError(w, err)
		return
	}
	d := backupScreen{Files: files, KeepAuto: h.BackupKeep, Personal: h.BackupPersonal, RestoreIf: restoreWord}
	for _, f := range files {
		if f.Kind == backup.KindAuto {
			t := f.Created
			d.LastAuto = &t
			break
		}
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "backup", pageData{Title: "Backup", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: d})
}

func (h *Handler) backupPage(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{"saved": "Backup saved on the server.", "deleted": "Backup deleted."}[r.URL.Query().Get("done")]
	h.renderBackup(w, r, http.StatusOK, notice, "")
}

// backupDownload makes a backup and sends it. It is a POST so that it needs the page's CSRF token: a backup is the
// whole site, so no other web page may be able to trigger one.
func (h *Handler) backupDownload(w http.ResponseWriter, r *http.Request) {
	personal := r.PostFormValue("personal") != ""
	f, cleanup, err := h.Backup.Temp(r.Context(), personal)
	if err != nil {
		log.Printf("backup download: %v", err)
		h.renderBackup(w, r, http.StatusInternalServerError, "", "The backup couldn't be made. Check the server's log.")
		return
	}
	defer cleanup()
	sendZip(w, r, f, h.Backup.DownloadName(personal))
}

func sendZip(w http.ResponseWriter, r *http.Request, f *os.File, name string) {
	hd := w.Header()
	hd.Set("Content-Type", "application/zip")
	hd.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, f)
}

func (h *Handler) backupSave(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Backup.Save(r.Context(), backup.KindManual, r.PostFormValue("personal") != ""); err != nil {
		log.Printf("backup save: %v", err)
		h.renderBackup(w, r, http.StatusInternalServerError, "", "The backup couldn't be saved. Check the server's log and that the backup folder has room.")
		return
	}
	http.Redirect(w, r, "/admin/backup?done=saved", http.StatusSeeOther)
}

func (h *Handler) backupFile(w http.ResponseWriter, r *http.Request) {
	f, info, err := h.Backup.Open(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	sendZip(w, r, f, info.Name)
}

func (h *Handler) backupDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.Backup.Remove(r.PathValue("name")); err != nil && !errors.Is(err, backup.ErrNoFile) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/backup?done=deleted", http.StatusSeeOther)
}

// --- restoring ---

type reviewScreen struct {
	Source   string // "upload:<token>" or "file:<name>"
	Label    string // what the owner chose
	Archive  *backup.Archive
	Word     string
	Result   *backup.Result
	Snapshot string // the safety backup made before restoring
}

func (h *Handler) renderReview(w http.ResponseWriter, r *http.Request, status int, errMsg string, d reviewScreen) {
	d.Word = restoreWord
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "backup_review", pageData{Title: "Restore a backup", User: id, CSRF: id.CSRF, Error: errMsg, Data: d})
}

// review checks the staged or saved file and shows what restoring it would do.
func (h *Handler) review(w http.ResponseWriter, r *http.Request, source, label string) {
	f, size, err := h.openSource(source)
	if err != nil {
		h.renderBackup(w, r, http.StatusNotFound, "", "That backup isn't there any more. Choose it again.")
		return
	}
	defer f.Close()
	a, err := h.Backup.Inspect(r.Context(), f, size)
	if err != nil {
		var inv *backup.Invalid
		if errors.As(err, &inv) {
			h.dropSource(source)
			h.renderBackup(w, r, http.StatusBadRequest, "", inv.Msg)
			return
		}
		log.Printf("backup inspect: %v", err)
		h.renderBackup(w, r, http.StatusInternalServerError, "", "The file couldn't be read. Check the server's log.")
		return
	}
	h.renderReview(w, r, http.StatusOK, "", reviewScreen{Source: source, Label: label, Archive: a})
}

func (h *Handler) openSource(source string) (*os.File, int64, error) {
	switch kind, rest, _ := strings.Cut(source, ":"); kind {
	case "upload":
		return h.Backup.OpenStaged(rest)
	case "file":
		f, info, err := h.Backup.Open(rest)
		return f, info.Size, err
	}
	return nil, 0, backup.ErrNoFile
}

// dropSource throws away an uploaded file that turned out not to be a backup (saved backups are left alone).
func (h *Handler) dropSource(source string) {
	if kind, rest, _ := strings.Cut(source, ":"); kind == "upload" {
		h.Backup.Unstage(rest)
	}
}

func (h *Handler) backupUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.renderBackup(w, r, http.StatusRequestEntityTooLarge, "", "That file is larger than a backup this site accepts.")
			return
		}
		h.renderBackup(w, r, http.StatusBadRequest, "", "The upload couldn't be read.")
		return
	}
	fhs := r.MultipartForm.File["backup"]
	if len(fhs) != 1 {
		h.renderBackup(w, r, http.StatusBadRequest, "", "Choose one backup file (a .zip).")
		return
	}
	up, err := fhs[0].Open()
	if err != nil {
		h.renderBackup(w, r, http.StatusBadRequest, "", "The upload couldn't be read.")
		return
	}
	defer up.Close()
	token, _, err := h.Backup.Stage(up)
	if err != nil {
		var inv *backup.Invalid
		if errors.As(err, &inv) {
			h.renderBackup(w, r, http.StatusBadRequest, "", inv.Msg)
			return
		}
		log.Printf("backup stage: %v", err)
		h.renderBackup(w, r, http.StatusInternalServerError, "", "The file couldn't be stored. Check the server's log and that the backup folder has room.")
		return
	}
	h.review(w, r, "upload:"+token, fhs[0].Filename)
}

func (h *Handler) backupReviewFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.review(w, r, "file:"+name, name)
}

// restoreMu keeps two restores from running at once in this process.
var restoreMu sync.Mutex

func (h *Handler) backupRestore(w http.ResponseWriter, r *http.Request) {
	source := r.PostFormValue("source")
	f, size, err := h.openSource(source)
	if err != nil {
		h.renderBackup(w, r, http.StatusNotFound, "", "That backup isn't there any more. Choose it again.")
		return
	}
	defer f.Close()

	again := func(status int, msg string) {
		a, ierr := h.Backup.Inspect(r.Context(), f, size)
		if ierr != nil {
			h.renderBackup(w, r, status, "", msg)
			return
		}
		h.renderReview(w, r, status, msg, reviewScreen{Source: source, Label: r.PostFormValue("label"), Archive: a})
	}
	if strings.TrimSpace(r.PostFormValue("confirm")) != restoreWord {
		again(http.StatusBadRequest, "Type "+restoreWord+" in capital letters to confirm.")
		return
	}
	if !restoreMu.TryLock() {
		again(http.StatusConflict, "A restore is already running. Wait for it to finish.")
		return
	}
	defer restoreMu.Unlock()

	opt := backup.Options{Personal: r.PostFormValue("personal") != "", UserID: auth.CurrentIdentity(r).UserID}
	// the way back: the site as it is now, saved first. If it can't be saved, nothing is restored.
	snap, err := h.Backup.Save(r.Context(), backup.KindPreRestore, opt.Personal)
	if err != nil {
		log.Printf("backup before restore: %v", err)
		again(http.StatusInternalServerError, "The safety backup of the current site couldn't be saved, so nothing was restored. Check the server's log and that the backup folder has room.")
		return
	}
	res, err := h.Backup.Restore(r.Context(), f, size, opt)
	if err != nil {
		var inv *backup.Invalid
		if errors.As(err, &inv) {
			again(http.StatusBadRequest, inv.Msg+" Nothing was changed.")
			return
		}
		log.Printf("restore: %v", err)
		again(http.StatusInternalServerError, "The restore failed and nothing was changed. Check the server's log.")
		return
	}
	h.dropSource(source)
	h.renderReview(w, r, http.StatusOK, "", reviewScreen{Label: r.PostFormValue("label"), Result: &res, Snapshot: snap.Name})
}
