package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/chrthal/accountant/internal/backup"
	"github.com/chrthal/accountant/internal/store"
)

// backupCommands are the subcommands next to the server (ADR 0019):
//
//	bookkeeper backup  [-dsn …] [-o FILE|DIR|-]
//	bookkeeper restore [-dsn …] FILE|-
//	bookkeeper verify  FILE|-
func backupCommand(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch args[0] {
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ExitOnError)
		dsn := fs.String("dsn", store.DSNFromEnv(), "database to back up (env: BOOKKEEPER_DSN)")
		out := fs.String("o", ".", "output: a file, a directory (a dated name is chosen), or - for stdout")
		fs.Parse(args[1:])
		return true, runBackup(ctx, *dsn, *out)

	case "restore":
		fs := flag.NewFlagSet("restore", flag.ExitOnError)
		dsn := fs.String("dsn", store.DSNFromEnv(), "EMPTY database to restore into (env: BOOKKEEPER_DSN)")
		fs.Parse(args[1:])
		if fs.NArg() != 1 {
			return true, fmt.Errorf("usage: bookkeeper restore [-dsn DSN] FILE")
		}
		return true, runRestore(ctx, *dsn, fs.Arg(0))

	case "verify":
		if len(args) != 2 {
			return true, fmt.Errorf("usage: bookkeeper verify FILE")
		}
		in, closeIn, err := openInput(args[1])
		if err != nil {
			return true, err
		}
		defer closeIn()
		sum, err := store.VerifyBackup(in)
		if err != nil {
			return true, err
		}
		printSummary("verified", sum)
		return true, nil
	}
	return false, nil
}

func runBackup(ctx context.Context, dsn, out string) error {
	st, err := store.Open(dsn)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if out == "-" {
		// Streamed, so it cannot be verified here: run `bookkeeper verify` on
		// the result. It is therefore not recorded as the last backup.
		sum, err := st.Backup(ctx, os.Stdout, version)
		if err == nil {
			printSummary("written to stdout (not verified: run `bookkeeper verify` on it)", sum)
		}
		return err
	}
	if fi, err := os.Stat(out); err == nil && fi.IsDir() {
		out = filepath.Join(out, backup.FileName(time.Now()))
	}
	sum, err := backup.ToFile(ctx, st, out, version)
	if err != nil {
		return err
	}
	printSummary("written and verified: "+out, sum)
	return nil
}

func runRestore(ctx context.Context, dsn, file string) error {
	in, closeIn, err := openInput(file)
	if err != nil {
		return err
	}
	defer closeIn()
	sum, err := store.Restore(ctx, dsn, in)
	if err != nil {
		return err
	}
	printSummary("restored and verified", sum)
	return nil
}

func openInput(name string) (io.Reader, func() error, error) {
	if name == "-" {
		return os.Stdin, func() error { return nil }, nil
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

// printSummary goes to stderr, so `backup -o -` keeps stdout for the data.
func printSummary(what string, sum store.BackupSummary) {
	fmt.Fprintf(os.Stderr, "backup %s\n  taken %s by version %s on %s, schema %s\n",
		what, sum.CreatedAt.Local().Format("2006-01-02 15:04:05"), sum.AppVersion, sum.Engine,
		sum.Migrations[len(sum.Migrations)-1])
	tables := make([]string, 0, len(sum.Rows))
	for t := range sum.Rows {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		fmt.Fprintf(os.Stderr, "  %-18s %6d rows\n", t, sum.Rows[t])
	}
}

// schedulerFromEnv configures the automatic daily backup (ADR 0019).
// BOOKKEEPER_BACKUP_DIR turns it on; BOOKKEEPER_BACKUP_KEEP (default 30) is
// how many daily files are kept.
func schedulerFromEnv(st *store.Store) (*backup.Scheduler, error) {
	dir := os.Getenv("BOOKKEEPER_BACKUP_DIR")
	if dir == "" {
		return nil, nil
	}
	keep := 30
	if v := os.Getenv("BOOKKEEPER_BACKUP_KEEP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("BOOKKEEPER_BACKUP_KEEP must be a positive number, got %q", v)
		}
		keep = n
	}
	return &backup.Scheduler{Store: st, Dir: dir, Every: 24 * time.Hour, Keep: keep, AppVersion: version}, nil
}
