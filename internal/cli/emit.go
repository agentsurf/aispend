package cli

import (
	"context"
	"io"

	"github.com/prabhuvmk/aispend/internal/collect"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/sink"
	"github.com/prabhuvmk/aispend/internal/store"
	"github.com/prabhuvmk/aispend/internal/timerange"
	"github.com/prabhuvmk/aispend/internal/ui"
)

// storeEmitter batches facts into a sink and records the resume cursor after
// every page, so an interrupted backfill loses at most one page.
type storeEmitter struct {
	ctx     context.Context
	batch   *sink.Batcher
	db      *store.DB
	vendor  string
	window  timerange.Range
	count   int
	printer func(fact.Fact)
}

func newStoreEmitter(ctx context.Context, db *store.DB, s sink.Sink,
	vendor string, r timerange.Range) *storeEmitter {
	return &storeEmitter{
		ctx:    ctx,
		batch:  sink.NewBatcher(s, 500),
		db:     db,
		vendor: vendor,
		window: r,
	}
}

func (e *storeEmitter) Emit(f fact.Fact) error {
	e.count++
	if e.printer != nil {
		e.printer(f)
	}
	return e.batch.Add(e.ctx, f)
}

// PageDone flushes what has been read so far and records where to resume.
//
// The order matters: facts are written first, then the cursor. Reversed, an
// interrupt between the two would advance past a page that was never stored,
// and the gap would be invisible afterwards.
func (e *storeEmitter) PageDone(cursor string) error {
	if err := e.batch.Flush(e.ctx); err != nil {
		return err
	}
	return e.db.SaveSyncState(store.SyncState{
		Vendor:      e.vendor,
		CoveredFrom: e.window.FromDay(),
		CoveredTo:   e.window.ToDay(),
		Cursor:      cursor,
	})
}

// Close flushes anything still buffered.
func (e *storeEmitter) Close() error { return e.batch.Flush(e.ctx) }

// Count is how many facts this emitter received.
func (e *storeEmitter) Count() int { return e.count }

// printEmitter writes facts to a writer without storing them.
func printEmitter(w io.Writer, caps ui.Caps, count *int) collect.Emitter {
	return collect.EmitterFunc(func(f fact.Fact) error {
		*count++
		_, err := io.WriteString(w, "  "+factLine(f, caps)+"\n")
		return err
	})
}
