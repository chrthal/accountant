package server

import (
	"context"
	"log"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/store"
)

// Belege from a bulk upload are read in the background (ADR 0042): the
// upload only stores the files, so it returns as soon as they are sent, and
// several are read at once. A row waits with "pending" set until it is read.

const pendingField = "pending"

// maxReaders caps the files read at once. Each tesseract run then uses one
// thread, which is faster in total than few runs that share all cores.
const maxReaders = 4

type receiptReader struct {
	running atomic.Bool
	again   atomic.Bool
}

func readerCount() int { return max(1, min(maxReaders, runtime.NumCPU())) }

// startReading reads the waiting Belege in the background, unless a run is
// already going; that run then looks again when it ends, so an upload during
// a run is not missed.
func (s *Server) startReading() {
	s.reader.again.Store(true)
	if !s.reader.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.reader.running.Store(false)
		for s.reader.again.Swap(false) {
			if n, err := s.readPending(context.Background()); err != nil {
				log.Printf("read receipts: %v (after %d)", err, n)
			} else if n > 0 {
				log.Printf("read receipts: %d", n)
			}
		}
	}()
}

func (s *Server) readingReceipts() bool { return s.reader.running.Load() }

// readPending reads every waiting Beleg not read yet and stores what it
// proposes in place of the pending mark.
func (s *Server) readPending(ctx context.Context) (int, error) {
	queued, err := s.store.ImportQueue(ctx, store.ReceiptQueue)
	if err != nil {
		return 0, err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return 0, err
	}
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		return 0, err
	}
	contacts, err := s.store.ListContacts(ctx)
	if err != nil {
		return 0, err
	}
	hist, err := s.loadHistory(ctx)
	if err != nil {
		return 0, err
	}

	work := make(chan store.QueuedImport)
	var done atomic.Int64
	var wg sync.WaitGroup
	readCtx := ocr.SingleThreaded(ctx)
	for range readerCount() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range work {
				_, data, err := s.store.QueuedOriginal(ctx, store.ReceiptQueue, q.ID)
				if err != nil {
					continue // discarded meanwhile
				}
				a, err := store.NewAttachment(q.Filename, data)
				if err != nil {
					continue
				}
				fileCtx, cancel := context.WithTimeout(readCtx, 2*ocr.Timeout)
				draft, _, note := s.readReceipt(fileCtx, data, q.Filename, settings)
				cancel()
				if err := s.store.UpdateQueuedFields(ctx, store.ReceiptQueue, q.ID,
					s.proposeReceipt(ctx, hist, draft, note, a, settings, cats, contacts)); err != nil {
					log.Printf("read receipt %d: %v", q.ID, err)
					continue
				}
				done.Add(1)
			}
		}()
	}
	for _, q := range queued {
		if q.Fields[pendingField] != "" {
			work <- q
		}
	}
	close(work)
	wg.Wait()
	return int(done.Load()), nil
}
