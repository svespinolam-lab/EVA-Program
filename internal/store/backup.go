package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"eva/internal/model"
)

// BackupInfo describes a snapshot; listing alone does not verify its contents.
type BackupInfo struct {
	Name      string `json:"name"`
	Path      string `json:"-"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"createdAt"`
	Verified  bool   `json:"verified"`
	Entries   int    `json:"entries"`
}

// Backup flushes pending telemetry and creates a verified, consistent snapshot.
func (s *Store) Backup(dir string) (BackupInfo, error) {
	s.Flush()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return BackupInfo{}, err
	}
	base := "eva-" + time.Now().UTC().Format("20060102T150405Z")
	var path string
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name += fmt.Sprintf("-%d", n)
		}
		path = filepath.Join(dir, name+".db")
		// SQLite accepts an empty destination. Reserve it exclusively so concurrent
		// backups cannot select the same name or overwrite an existing snapshot.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return BackupInfo{}, err
		}
		if err := f.Close(); err != nil {
			return BackupInfo{}, errors.Join(err, os.Remove(path))
		}
		break
	}
	if _, err := s.W.Exec(`VACUUM INTO ?`, path); err != nil {
		return BackupInfo{}, errors.Join(fmt.Errorf("backup snapshot: %w", err), os.Remove(path))
	}
	info, err := VerifyBackup(path)
	if err != nil {
		return info, errors.Join(err, os.Remove(path))
	}
	return info, nil
}

// VerifyBackup checks SQLite integrity and every black-box hash-chain link.
func VerifyBackup(path string) (info BackupInfo, err error) {
	info = BackupInfo{Name: filepath.Base(path), Path: path}
	fi, err := os.Stat(path)
	if err != nil {
		return info, err
	}
	if !fi.Mode().IsRegular() {
		return info, fmt.Errorf("backup is not a regular file: %s", path)
	}
	info.Bytes, info.CreatedAt = fi.Size(), fi.ModTime().UnixMilli()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return info, err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			info.Verified = false
		}
	}()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return info, fmt.Errorf("backup integrity_check: %w", err)
	}
	if integrity != "ok" {
		return info, fmt.Errorf("backup integrity_check: %s", integrity)
	}
	rep, err := verifyBackupChain(db)
	info.Entries = rep.Entries
	if err != nil {
		return info, fmt.Errorf("backup chain: %w", err)
	}
	if !rep.OK {
		return info, fmt.Errorf("backup chain broken at entry %d", rep.BrokenAt)
	}
	info.Verified = true
	return info, nil
}

// verifyBackupChain mirrors Store.VerifyChain for a separate database handle.
func verifyBackupChain(db *sql.DB) (ChainReport, error) {
	rows, err := db.Query(`SELECT id, ts, kind, COALESCE(crew_id,''), COALESCE(ref,''), payload, prev_hash, hash FROM blackbox_chain ORDER BY id`)
	if err != nil {
		return ChainReport{}, err
	}
	defer rows.Close()
	rep := ChainReport{OK: true, LastHash: "GENESIS", Checked: Now()}
	prev := "GENESIS"
	for rows.Next() {
		var e model.ChainEntry
		if err := rows.Scan(&e.ID, &e.T, &e.Kind, &e.CrewID, &e.Ref, &e.Payload, &e.PrevHash, &e.Hash); err != nil {
			return rep, err
		}
		rep.Entries++
		if e.PrevHash != prev || chainHash(prev, e.T, e.Kind, e.CrewID, e.Ref, e.Payload) != e.Hash {
			if rep.OK {
				rep.OK = false
				rep.BrokenAt = e.ID
			}
		}
		prev = e.Hash
	}
	rep.LastHash = prev
	return rep, rows.Err()
}

// ListBackups lists eva-*.db regular files by modification time, newest first.
// It does not open or verify the databases. A missing directory is empty.
func ListBackups(dir string) ([]BackupInfo, error) {
	out := make([]BackupInfo, 0)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		match, _ := filepath.Match("eva-*.db", entry.Name())
		if !match {
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if fi.Mode().IsRegular() {
			out = append(out, BackupInfo{Name: entry.Name(), Path: filepath.Join(dir, entry.Name()), Bytes: fi.Size(), CreatedAt: fi.ModTime().UnixMilli()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].Name > out[j].Name
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

// PruneBackups removes older snapshots, keeping at least one, and returns removed paths.
func PruneBackups(dir string, keep int) (removed []string, err error) {
	if keep < 1 {
		keep = 1
	}
	backups, err := ListBackups(dir)
	if err != nil {
		return nil, err
	}
	for i := keep; i < len(backups); i++ {
		if err := os.Remove(backups[i].Path); err != nil {
			return removed, err
		}
		removed = append(removed, backups[i].Path)
	}
	return removed, nil
}

// Restore copies a verified backup into dbPath, preserving the previous database
// and its WAL/SHM files under a timestamped name. No Store or other database
// connection may be open on dbPath during this operation.
func Restore(backupPath, dbPath string) (movedTo string, err error) {
	if _, err := VerifyBackup(backupPath); err != nil {
		return "", err
	}
	source, err := os.Open(backupPath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	old, statErr := os.Stat(dbPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if statErr == nil {
		srcInfo, err := source.Stat()
		if err != nil {
			return "", err
		}
		if !old.Mode().IsRegular() || os.SameFile(srcInfo, old) {
			return "", fmt.Errorf("restore destination must be a regular file distinct from the backup")
		}
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return "", err
	}
	staged, err := os.CreateTemp(filepath.Dir(dbPath), ".eva-restore-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(staged.Name())
	_, copyErr := io.Copy(staged, source)
	syncErr := staged.Sync()
	closeErr := staged.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", fmt.Errorf("stage restore: %w", err)
	}
	suffixes := []string{"", "-wal", "-shm"}
	if statErr == nil {
		base := dbPath + ".pre-restore-" + time.Now().UTC().Format("20060102T150405Z")
		for n := 1; ; n++ {
			movedTo = base
			if n > 1 {
				movedTo += fmt.Sprintf("-%d", n)
			}
			available := true
			for _, suffix := range suffixes {
				if _, err := os.Lstat(movedTo + suffix); err == nil {
					available = false
				} else if !errors.Is(err, os.ErrNotExist) {
					return "", err
				}
			}
			if available {
				break
			}
		}
	} else {
		// Orphaned sidecars must not be applied to the restored snapshot.
		for _, suffix := range suffixes[1:] {
			if _, err := os.Lstat(dbPath + suffix); err == nil {
				return "", fmt.Errorf("restore destination has orphaned sidecar: %s", dbPath+suffix)
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
	}
	var moved []string
	rollback := func(cause error) (string, error) {
		for i := len(moved) - 1; i >= 0; i-- {
			cause = errors.Join(cause, os.Rename(movedTo+moved[i], dbPath+moved[i]))
		}
		return movedTo, cause
	}
	if movedTo != "" {
		for _, suffix := range suffixes {
			if err := os.Rename(dbPath+suffix, movedTo+suffix); err != nil {
				if suffix != "" && errors.Is(err, os.ErrNotExist) {
					continue
				}
				return rollback(err)
			}
			moved = append(moved, suffix)
		}
	}
	if err := os.Rename(staged.Name(), dbPath); err != nil {
		return rollback(err)
	}
	return movedTo, nil
}
