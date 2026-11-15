package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eva/internal/model"
)

func openBackupTestStore(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SeedIfEmpty(); err != nil {
		st.Close()
		t.Fatal(err)
	}
	return st
}

func copyBackupTestFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tamperBackupTestFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE blackbox_chain SET payload='x' WHERE id=(SELECT MIN(id) FROM blackbox_chain)`); err != nil {
		t.Fatal(err)
	}
}

func TestBackupAndVerify(t *testing.T) {
	dir := t.TempDir()
	st := openBackupTestStore(t, filepath.Join(dir, "live.db"))
	defer st.Close()
	st.Chain("BACKUP_TEST", "", "", map[string]bool{"ok": true})
	const ts = int64(1_800_000_000_000)
	st.AddTelemetry(model.Sample{CrewID: "AST-001", T: ts, HR: model.F(70)}, "", 10, "STABLE", "device")
	backupDir := filepath.Join(dir, "snapshots with ' quotes")
	info, err := st.Backup(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Verified || info.Entries < 1 || info.Bytes <= 0 || info.CreatedAt <= 0 || info.Name != filepath.Base(info.Path) {
		t.Fatalf("backup metadata: %+v", info)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBackup(info.Path)
	if err != nil || !verified.Verified || verified.Entries != info.Entries {
		t.Fatalf("verify: %+v, %v", verified, err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(info.Path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hr float64
	if err := db.QueryRow(`SELECT hr FROM telemetry WHERE crew_id='AST-001' AND ts=?`, ts).Scan(&hr); err != nil || hr != 70 {
		t.Fatalf("pending telemetry was not backed up: hr=%v, err=%v", hr, err)
	}
	list, err := ListBackups(backupDir)
	if err != nil || len(list) != 1 || list[0].Path != info.Path || list[0].Verified {
		t.Fatalf("list: %+v, %v", list, err)
	}
	second, err := st.Backup(backupDir)
	if err != nil || second.Path == info.Path {
		t.Fatalf("second backup overwrote the first: %+v, %v", second, err)
	}
}

func TestVerifyBackupDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	st := openBackupTestStore(t, filepath.Join(dir, "live.db"))
	defer st.Close()
	st.Chain("BACKUP_TEST", "AST-001", "a", "original")
	info, err := st.Backup(dir)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "tampered.db")
	copyBackupTestFile(t, info.Path, copyPath)
	tamperBackupTestFile(t, copyPath)
	bad, err := VerifyBackup(copyPath)
	if err == nil || bad.Verified || !strings.Contains(err.Error(), "chain broken") {
		t.Fatalf("tampered backup accepted: %+v, %v", bad, err)
	}
	if good, err := VerifyBackup(info.Path); err != nil || !good.Verified {
		t.Fatalf("original backup changed: %+v, %v", good, err)
	}
}

func TestRestoreBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	const ts = int64(1_800_000_000_000)
	var backup BackupInfo
	func() {
		st := openBackupTestStore(t, dbPath)
		defer st.Close()
		st.Chain("RESTORE_TEST", "AST-001", "A", "data A")
		st.AddTelemetry(model.Sample{CrewID: "AST-001", T: ts, HR: model.F(70)}, "", 10, "STABLE", "device")
		st.Flush()
		var err error
		backup, err = st.Backup(filepath.Join(dir, "backups"))
		if err != nil {
			t.Fatal(err)
		}
	}()
	func() {
		st := openBackupTestStore(t, dbPath)
		defer st.Close()
		st.Chain("RESTORE_TEST", "AST-001", "B", "data B")
		st.AddTelemetry(model.Sample{CrewID: "AST-001", T: ts + 1000, HR: model.F(80)}, "", 10, "STABLE", "device")
		st.Flush()
	}()
	// Closed databases normally remove their sidecars; use markers to check that
	// any remaining sidecars are preserved and cannot contaminate the restore.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(dbPath+suffix, []byte(suffix), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	moved, err := Restore(backup.Path, dbPath)
	if err != nil || !strings.HasPrefix(moved, dbPath+".pre-restore-") {
		t.Fatalf("restore: %q, %v", moved, err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		data, err := os.ReadFile(moved + suffix)
		if err != nil || string(data) != suffix {
			t.Fatalf("preserved sidecar %s: %q, %v", suffix, data, err)
		}
	}
	if _, err := os.Stat(backup.Path); err != nil {
		t.Fatal("backup was not retained:", err)
	}
	st := openBackupTestStore(t, dbPath)
	defer st.Close()
	for _, item := range []struct {
		ref  string
		ts   int64
		want int
	}{{"A", ts, 1}, {"B", ts + 1000, 0}} {
		var count int
		if err := st.R.QueryRow(`SELECT COUNT(*) FROM blackbox_chain WHERE ref=?`, item.ref).Scan(&count); err != nil || count != item.want {
			t.Fatalf("chain data %s: count=%d, err=%v", item.ref, count, err)
		}
		if err := st.R.QueryRow(`SELECT COUNT(*) FROM telemetry WHERE crew_id='AST-001' AND ts=?`, item.ts).Scan(&count); err != nil || count != item.want {
			t.Fatalf("telemetry data %s: count=%d, err=%v", item.ref, count, err)
		}
	}
	if rep, err := st.VerifyChain(); err != nil || !rep.OK {
		t.Fatalf("restored chain: %+v, %v", rep, err)
	}
	newPath := filepath.Join(dir, "new", "restored.db")
	if moved, err := Restore(backup.Path, newPath); err != nil || moved != "" {
		t.Fatalf("restore without previous database: %q, %v", moved, err)
	}
	if info, err := VerifyBackup(newPath); err != nil || !info.Verified {
		t.Fatalf("new restored database: %+v, %v", info, err)
	}
}

func TestPruneBackups(t *testing.T) {
	for _, keep := range []int{2, 0, -1, 10} {
		t.Run(fmt.Sprintf("keep_%d", keep), func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < 4; i++ {
				path := filepath.Join(dir, fmt.Sprintf("eva-%d.db", i))
				if err := os.WriteFile(path, []byte("listing does not verify"), 0o600); err != nil {
					t.Fatal(err)
				}
				at := time.Unix(1_700_000_000+int64(i), 0)
				if err := os.Chtimes(path, at, at); err != nil {
					t.Fatal(err)
				}
			}
			unrelated := filepath.Join(dir, "other.db")
			if err := os.WriteFile(unrelated, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "eva-directory.db"), 0o755); err != nil {
				t.Fatal(err)
			}
			removed, err := PruneBackups(dir, keep)
			want := min(4, max(1, keep))
			if err != nil || len(removed) != 4-want {
				t.Fatalf("prune: %v, %v", removed, err)
			}
			list, err := ListBackups(dir)
			if err != nil || len(list) != want {
				t.Fatalf("remaining: %+v, %v", list, err)
			}
			for i, info := range list {
				if info.Name != fmt.Sprintf("eva-%d.db", 3-i) || info.Verified {
					t.Fatalf("unexpected listing: %+v", list)
				}
			}
			for _, path := range removed {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("removed file exists: %s, %v", path, err)
				}
			}
			if _, err := os.Stat(unrelated); err != nil {
				t.Fatal(err)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if list, err := ListBackups(missing); err != nil || len(list) != 0 {
		t.Fatalf("missing directory: %+v, %v", list, err)
	}
}

func TestRestoreRefusesTamperedBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	var backup BackupInfo
	func() {
		st := openBackupTestStore(t, dbPath)
		defer st.Close()
		st.Chain("RESTORE_TEST", "", "", "original")
		var err error
		backup, err = st.Backup(dir)
		if err != nil {
			t.Fatal(err)
		}
	}()
	copyPath := filepath.Join(dir, "tampered.db")
	copyBackupTestFile(t, backup.Path, copyPath)
	tamperBackupTestFile(t, copyPath)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := Restore(copyPath, dbPath); err == nil || moved != "" {
		t.Fatalf("tampered restore accepted: %q, %v", moved, err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("destination changed: %v", err)
	}
	files, err := filepath.Glob(dbPath + ".pre-restore-*")
	if err != nil || len(files) != 0 {
		t.Fatalf("destination was renamed: %v, %v", files, err)
	}
}

func TestBackupRemovesUnverifiedSnapshot(t *testing.T) {
	dir := t.TempDir()
	st := openBackupTestStore(t, filepath.Join(dir, "live.db"))
	defer st.Close()
	st.Chain("BACKUP_TEST", "", "", "original")
	if _, err := st.W.Exec(`UPDATE blackbox_chain SET payload='x' WHERE id=(SELECT MIN(id) FROM blackbox_chain)`); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")
	if info, err := st.Backup(backupDir); err == nil || info.Verified {
		t.Fatalf("invalid snapshot accepted: %+v, %v", info, err)
	}
	list, err := ListBackups(backupDir)
	if err != nil || len(list) != 0 {
		t.Fatalf("invalid snapshot retained: %+v, %v", list, err)
	}
}
