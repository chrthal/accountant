package store_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

func opener(b []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
}

// ADR 0030: a backup goes into a fresh, running installation, also across
// engines; the default categories make way for the backed-up ones.
func TestRestoreIntoARunningFreshInstallation(t *testing.T) {
	for srcEngine, srcDSN := range dsns(t) {
		src, err := store.Open(srcDSN)
		if err != nil {
			t.Fatal(err)
		}
		seedBooks(t, src)
		var file, again bytes.Buffer
		if _, err := src.Backup(context.Background(), &file, "test"); err != nil {
			t.Fatal(err)
		}
		src.Backup(context.Background(), &again, "test")
		src.Close()

		for dstEngine, dstDSN := range dsns(t) {
			t.Run(srcEngine+"_to_"+dstEngine, func(t *testing.T) {
				ctx := context.Background()
				dst, err := store.Open(dstDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer dst.Close()
				if added, err := dst.SeedDefaultCategories(ctx); err != nil || !added {
					t.Fatalf("seed: %v %v", added, err)
				}
				// The daily backup runs at the first start and records its time.
				if err := dst.MarkBackup(ctx, time.Now()); err != nil {
					t.Fatal(err)
				}
				if fresh, err := dst.Fresh(ctx); err != nil || !fresh {
					t.Fatalf("a new installation is not fresh: %v %v", fresh, err)
				}

				if _, err := dst.RestoreRunning(ctx, opener(file.Bytes())); err != nil {
					t.Fatalf("restore: %v", err)
				}
				var round bytes.Buffer
				if _, err := dst.Backup(ctx, &round, "test"); err != nil {
					t.Fatal(err)
				}
				if a, b := tableHashes(t, again.Bytes()), tableHashes(t, round.Bytes()); a != b {
					t.Errorf("restored books differ from the original:\n%s\n%s", a, b)
				}
				if id, err := dst.CreateEntry(ctx, sampleEntry()); err != nil || id != 6 {
					t.Errorf("new booking after restore: id %d, err %v", id, err)
				}

				if _, err := dst.RestoreRunning(ctx, opener(file.Bytes())); !errors.Is(err, store.ErrNotFresh) {
					t.Errorf("a second restore = %v, want ErrNotFresh", err)
				}
			})
		}
	}
}

// Nothing changes when the file is damaged or the installation is in use.
func TestRestoreRunningRefusesAndChangesNothing(t *testing.T) {
	var file bytes.Buffer
	openEach(t, func(t *testing.T, src *store.Store) {
		if file.Len() == 0 {
			seedBooks(t, src)
			if _, err := src.Backup(context.Background(), &file, "test"); err != nil {
				t.Fatal(err)
			}
		}
	})
	openEach(t, func(t *testing.T, dst *store.Store) {
		ctx := context.Background()
		dst.SeedDefaultCategories(ctx)

		damaged := append([]byte(nil), file.Bytes()[:file.Len()-40]...)
		if _, err := dst.RestoreRunning(ctx, opener(damaged)); err == nil {
			t.Error("a damaged backup was restored")
		}
		if cats, _ := dst.ListCategories(ctx); len(cats) != len(store.DefaultCategories) {
			t.Errorf("after a refused restore there are %d categories", len(cats))
		}

		// A changed default category is the user's data.
		cats, _ := dst.ListCategories(ctx)
		c := cats[0]
		c.Keywords = "meine"
		if err := dst.UpdateCategory(ctx, c); err != nil {
			t.Fatal(err)
		}
		if fresh, _ := dst.Fresh(ctx); fresh {
			t.Error("an edited default category still counts as fresh")
		}
		if _, err := dst.RestoreRunning(ctx, opener(file.Bytes())); !errors.Is(err, store.ErrNotFresh) {
			t.Errorf("restore over an edited category = %v, want ErrNotFresh", err)
		}
	})
}
