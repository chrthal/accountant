// Command bookkeeper serves the kmu-buchhaltung web application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chrthal/accountant/internal/llm"
	"github.com/chrthal/accountant/internal/notify"
	"github.com/chrthal/accountant/internal/server"
	"github.com/chrthal/accountant/internal/store"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if handled, err := backupCommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "bookkeeper:", err)
			os.Exit(1)
		}
		return
	}

	addr := flag.String("addr", ":8080", "listen address")
	dsn := flag.String("dsn", store.DSNFromEnv(),
		"database DSN: postgres://user:pass@host:5432/bookkeeper?sslmode=disable, "+
			"or sqlite://path.db (env: BOOKKEEPER_DSN)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	password, err := passwordFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(*addr, *dsn, password); err != nil {
		log.Fatal(err)
	}
}

// passwordFromEnv reads the login password (ADR 0017) from
// BOOKKEEPER_PASSWORD, or from the file named by BOOKKEEPER_PASSWORD_FILE
// (for container secrets, which keeps it out of `docker inspect`).
func passwordFromEnv() (string, error) {
	if path := os.Getenv("BOOKKEEPER_PASSWORD_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read BOOKKEEPER_PASSWORD_FILE: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return os.Getenv("BOOKKEEPER_PASSWORD"), nil
}

// loopbackOnly reports whether addr only listens on the local machine.
func loopbackOnly(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func run(addr, dsn, password string) error {
	st, err := store.Open(dsn)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if added, err := st.SeedDefaultCategories(context.Background()); err != nil {
		log.Printf("default categories: %v", err)
	} else if added {
		log.Printf("new installation: added %d default categories", len(store.DefaultCategories))
	}
	if n, err := st.BackfillCounterparties(context.Background()); err != nil {
		log.Printf("split bank text: %v", err)
	} else if n > 0 {
		log.Printf("split the counterparty out of %d earlier bank transaction(s)", n)
	}

	sched, err := schedulerFromEnv(st)
	if err != nil {
		return err
	}
	opts := []server.Option{server.WithPassword(password)}
	if url := os.Getenv("BOOKKEEPER_LLM_URL"); url != "" {
		c := llm.New(url, os.Getenv("BOOKKEEPER_LLM_MODEL"))
		opts = append(opts, server.WithLLM(c))
		log.Printf("booking suggestions from %s at %s", c.Model, c.URL)
	}
	if sched != nil {
		opts = append(opts, server.WithBackupSchedule(sched.Dir, sched.Keep))
	}
	envMailer, envMail, err := notify.FromEnv()
	if err != nil {
		return err
	}
	if envMail {
		opts = append(opts, server.WithEnvMailer(envMailer, envMailer.Describe()))
	}
	// The notifier always runs: a mail server set in the web UI later is
	// picked up on its next run, without a restart (ADR 0040).
	notifier := &notify.Notifier{Store: st, PublicURL: os.Getenv("BOOKKEEPER_PUBLIC_URL"), Log: log.Default(),
		Resolve: func(ctx context.Context) (notify.Mailer, error) {
			m, _, err := notify.Resolve(ctx, st)
			if m == nil || err != nil {
				return nil, err
			}
			return m, nil
		}}

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(st, version, opts...),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Shut down cleanly on SIGINT/SIGTERM so the container stops without
	// killing an in-flight request mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go notifier.Run(ctx)
	if envMail {
		log.Printf("email notifications via %s", envMailer.Describe())
	}
	if sched != nil {
		sched.Log = log.Default()
		sched.OnError = func(err error) { notifier.NotifyBackupFailed(ctx, err) }
		go sched.Run(ctx)
		log.Printf("daily backups to %s, keeping %d", sched.Dir, sched.Keep)
	}

	errc := make(chan error, 1)
	if password == "" && !loopbackOnly(addr) {
		// Not fatal: a reverse proxy may already authenticate. But books
		// reachable on a network without a password should not go unnoticed.
		log.Printf("WARNING: no BOOKKEEPER_PASSWORD set and listening on %s — anyone who can reach this port can read and write the books", addr)
	}

	go func() {
		log.Printf("bookkeeper %s listening on %s (%s, login %s)", version, addr, st.Dialect,
			map[bool]string{true: "on", false: "off"}[password != ""])
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
