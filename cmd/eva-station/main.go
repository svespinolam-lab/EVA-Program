// EVA Station — cabin/base program for biomedical EVA monitoring.
//
// One executable: Go server + SQLite database + PC program + mobile app.
// It runs on the cabin PC with no internet; the explorers' phones connect
// over the local Wi-Fi (PC hotspot) and open http://<PC-IP>:8080/app/.
//
//	eva-station.exe                  start on port 8080 (data in ./data)
//	eva-station.exe -port 9090       another port
//	eva-station.exe -web ./web       serve the front-ends from disk (edit live)
//	eva-station.exe -virtual all     virtual suits for every explorer without a phone
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
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
	"eva/web"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	dataDir := flag.String("data", "data", "folder for the SQLite database")
	webDir := flag.String("web", "", "serve app/, pc/ and shared/ from this folder instead of the embedded copy (default: ./web if it exists)")
	virtual := flag.String("virtual", "AST-002", "crew ids with a virtual suit at start-up: comma list, 'all' or 'none'")
	tlsCert := flag.String("tls-cert", "", "TLS certificate PEM (required for phone BLE in Chrome)")
	tlsKey := flag.String("tls-key", "", "TLS private key PEM")
	flag.Parse()

	st, err := store.Open(filepath.Join(*dataDir, "eva-station.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if seeded, err := st.SeedIfEmpty(); err != nil {
		log.Fatalf("seed: %v", err)
	} else if seeded {
		log.Printf("new database created with the demonstration crew")
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

	hub := server.NewHub()
	srv := &server.Server{Eng: eng, St: st, Sim: sm, Hub: hub, Web: files, Port: *port}
	st.OnCommit = srv.OnCommit
	eng.SetEmitter(hub)
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

	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("both -tls-cert and -tls-key must be provided together")
	}
	scheme := "http"
	if *tlsCert != "" { scheme = "https" }
	httpSrv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		var err error
		if *tlsCert != "" {
			err = httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	fmt.Println()
	fmt.Println("  EVA Station " + server.Version + "  ·  biomedical EVA monitoring")
	fmt.Println("  ─────────────────────────────────────────────────────────")
	fmt.Printf("  PC program      %s://localhost:%d\n", scheme, *port)
	for _, u := range server.LANURLs(*port) {
		fmt.Printf("  Phones (Wi-Fi)  %s://%s/app/\n", scheme, strings.TrimPrefix(u, "http://"))
	}
	fmt.Printf("  Database        %s\n", filepath.Join(*dataDir, "eva-station.db"))
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
	_ = st.Close()
}
