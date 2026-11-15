// EVA Station — cabin/base program for biomedical EVA monitoring.
//
// One executable: Go server + SQLite database + PC program + mobile app.
// It runs on the cabin PC with no internet; the explorers' phones connect
// over the local Wi-Fi (PC hotspot) and open http://<PC-IP>:8080/app/.
//
//	eva-station.exe                          start (HTTP 8080, HTTPS 8443, data in ./data)
//	eva-station.exe -port 9090               another HTTP port
//	eva-station.exe -web ./web               serve the front-ends from disk (edit live)
//	eva-station.exe -virtual all             virtual suits for every explorer without a phone
//	eva-station.exe -pairing open            demo only: pair phones without a code
//	eva-station.exe -create-user "Dr. Vega" -role surgeon     print a personal console link
//	eva-station.exe -restore data\backups\eva-20261115T071530Z.db   restore a verified backup
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"eva/internal/engine"
	"eva/internal/server"
	"eva/internal/sim"
	"eva/internal/store"
	"eva/internal/tlsutil"
	"eva/web"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	httpsPort := flag.Int("https-port", 8443, "HTTPS port (self-signed local certificate unless -tls-cert/-tls-key are given; 0 = off)")
	dataDir := flag.String("data", "data", "folder for the SQLite database, backups and the TLS certificate")
	webDir := flag.String("web", "", "serve app/, pc/ and shared/ from this folder instead of the embedded copy (default: ./web if it exists)")
	virtual := flag.String("virtual", "AST-002", "crew ids with a virtual suit at start-up: comma list, 'all' or 'none'")
	tlsCert := flag.String("tls-cert", "", "TLS certificate PEM (e.g. a CA-signed certificate for phone BLE in Chrome)")
	tlsKey := flag.String("tls-key", "", "TLS private key PEM")
	opToken := flag.String("operator-token", "", "bootstrap operator token (default: EVA_OPERATOR_TOKEN or <data>/operator.token, created on first start)")
	pairing := flag.String("pairing", "code", "phone pairing: 'code' (one-time code from the console, default) or 'open' (demo only, no code)")
	backupEvery := flag.Duration("backup-every", 6*time.Hour, "automatic verified backup interval (0 = off)")
	backupKeep := flag.Int("backup-keep", 10, "automatic backups to keep")
	restore := flag.String("restore", "", "restore this verified backup into the database, then exit (stop the station first)")
	createUser := flag.String("create-user", "", "create a console user with this name, print the access link and exit")
	role := flag.String("role", "surgeon", "role for -create-user: operator or surgeon")
	flag.Parse()

	dbPath := filepath.Join(*dataDir, "eva-station.db")
	backupDir := filepath.Join(*dataDir, "backups")

	if *restore != "" {
		moved, err := store.Restore(*restore, dbPath)
		if err != nil {
			log.Fatalf("restore: %v", err)
		}
		fmt.Printf("Restored %s into %s\n", *restore, dbPath)
		if moved != "" {
			fmt.Printf("The previous database was kept as %s\n", moved)
		}
		return
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if st.SchemaFrom != st.SchemaTo {
		log.Printf("database upgraded from schema v%d to v%d (safety copy in %s)", st.SchemaFrom, st.SchemaTo, backupDir)
	}
	if seeded, err := st.SeedIfEmpty(); err != nil {
		log.Fatalf("seed: %v", err)
	} else if seeded {
		log.Printf("new database created with the demonstration crew")
	}

	if *createUser != "" {
		u, token, err := st.CreateUser(*createUser, *role, "station-cli")
		if err != nil {
			log.Fatalf("create user: %v", err)
		}
		st.Chain("USER_CREATED", "", u.ID, map[string]any{"name": u.Name, "role": u.Role, "by": "station-cli"})
		_ = st.Close()
		fmt.Printf("User %s (%s) created. Personal console link (shown only once, keep it private):\n", u.Name, u.Role)
		fmt.Printf("  http://<station-ip>:%d/?token=%s\n", *port, token)
		return
	}

	eng, err := engine.New(st)
	if err != nil {
		log.Fatalf("engine: %v", err)
	}
	crew, _ := st.ListCrew()
	bases, _ := st.Baselines()
	sm := sim.New(eng, crew, bases)
	for _, c := range crew {
		if !c.IsEVA() {
			continue
		}
		switch v := strings.ToLower(*virtual); {
		case v == "all":
			sm.SetEnabled(c.ID, true)
		case v == "none":
		default:
			for _, id := range strings.Split(*virtual, ",") {
				if strings.EqualFold(strings.TrimSpace(id), c.ID) {
					sm.SetEnabled(c.ID, true)
				}
			}
		}
	}

	var files fs.FS = web.FS()
	source := "embedded"
	dir := *webDir
	if dir == "" {
		if fi, err := os.Stat("web/pc"); err == nil && fi.IsDir() {
			dir = "web"
		}
	}
	if dir != "" {
		files = os.DirFS(dir)
		source = dir + " (live from disk)"
	}

	token, err := server.LoadOperatorToken(*dataDir, *opToken)
	if err != nil {
		log.Fatalf("operator token: %v", err)
	}
	if *pairing != "code" && *pairing != "open" {
		log.Fatalf("-pairing must be 'code' or 'open'")
	}

	hub := server.NewHub()
	srv := &server.Server{Eng: eng, St: st, Sim: sm, Hub: hub, Web: files, Port: *port, OperatorToken: token,
		OpenPairing: *pairing == "open", DataDir: *dataDir}
	st.OnCommit = srv.OnCommit
	eng.SetEmitter(srv.Emitter()) // instructions, buddy alerts and aborts survive phone disconnections
	eng.SetVirtualSuits(sm)

	stop := make(chan struct{})
	go sm.Run(stop)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				eng.Tick()
			}
		}
	}()
	if *backupEvery > 0 {
		go autoBackup(st, backupDir, *backupEvery, *backupKeep, stop)
	}

	lan := server.LANURLs(*port)
	var lanHosts []string
	for _, u := range lan {
		if x, err := url.Parse(u); err == nil {
			lanHosts = append(lanHosts, x.Hostname())
		}
	}

	// HTTPS (encrypted link) next to plain HTTP. Without a CA-signed
	// certificate the browser shows a warning the first time: compare the
	// fingerprint printed below with the one in the browser.
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("both -tls-cert and -tls-key must be provided together")
	}
	certFile, keyFile := *tlsCert, *tlsKey
	if *httpsPort > 0 && certFile == "" {
		c, k, created, err := tlsutil.EnsureCert(filepath.Join(*dataDir, "tls"), lanHosts)
		if err != nil {
			log.Printf("HTTPS disabled: %v", err)
			*httpsPort = 0
		} else {
			certFile, keyFile = c, k
			if created {
				log.Printf("new local TLS certificate in %s", filepath.Dir(c))
			}
		}
	}
	if *httpsPort > 0 {
		srv.HTTPSPort = *httpsPort
		srv.TLSFingerprint, _ = tlsutil.Fingerprint(certFile)
	}

	handler := srv.Handler()
	httpSrv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()
	var httpsSrv *http.Server
	if *httpsPort > 0 {
		httpsSrv = &http.Server{Addr: fmt.Sprintf(":%d", *httpsPort), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := httpsSrv.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("HTTPS server: %v", err)
			}
		}()
	}

	fmt.Println()
	fmt.Println("  EVA Station " + server.Version + "  ·  biomedical EVA monitoring")
	fmt.Println("  ─────────────────────────────────────────────────────────")
	fmt.Printf("  PC program      http://localhost:%d\n", *port)
	for _, h := range lanHosts {
		fmt.Printf("  Phones (Wi-Fi)  http://%s:%d/app/\n", h, *port)
		if *httpsPort > 0 {
			fmt.Printf("  Phones (HTTPS)  https://%s:%d/app/\n", h, *httpsPort)
		}
	}
	if len(lanHosts) > 0 {
		// The console is open on this PC; other devices need the operator link.
		fmt.Printf("  Operator link   http://%s:%d/?operator=%s   (console from another device; keep it private)\n", lanHosts[0], *port, token)
	}
	if *pairing == "open" {
		fmt.Println("  Pairing         OPEN (demo): phones are paired without a code")
	} else {
		fmt.Println("  Pairing         phones need a one-time code: console → Simulator → Pair a phone")
	}
	if srv.TLSFingerprint != "" {
		fmt.Printf("  TLS SHA-256     %s\n", srv.TLSFingerprint)
	}
	fmt.Printf("  Database        %s (schema v%d)\n", dbPath, st.SchemaVersion())
	if *backupEvery > 0 {
		fmt.Printf("  Backups         every %s, keep %d, in %s\n", backupEvery.String(), *backupKeep, backupDir)
	}
	fmt.Printf("  Web files       %s\n", source)
	fmt.Println("  Press Ctrl+C to stop.")
	fmt.Println()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\n  Stopping EVA Station…")
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if httpsSrv != nil {
		_ = httpsSrv.Shutdown(ctx)
	}
	_ = st.Close()
}

// autoBackup writes a verified backup now (if the newest one is older than
// the interval) and then every interval, keeping the newest `keep`.
func autoBackup(st *store.Store, dir string, every time.Duration, keep int, stop <-chan struct{}) {
	run := func() {
		info, err := st.Backup(dir)
		if err != nil {
			log.Printf("backup failed: %v", err)
			return
		}
		st.Chain("BACKUP", "", info.Name, map[string]any{"bytes": info.Bytes, "entries": info.Entries, "by": "auto"})
		_, _ = store.PruneBackups(dir, keep)
	}
	if bs, err := store.ListBackups(dir); err != nil || len(bs) == 0 || time.Since(time.UnixMilli(bs[0].CreatedAt)) > every {
		run()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			run()
		}
	}
}
