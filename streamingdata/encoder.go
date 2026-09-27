package streamingdata

import (
	"encoding/json"
	"fmt"

	"github.com/sharedcode/joltrin/v5/btree"
)

// Encoder writes JSON values to chunked storage by delegating to json.Encoder over a custom writer.
type Encoder[TK btree.Ordered] struct {
	jsonEncoder *json.Encoder
	w           *writer[TK]
}

func newEncoder[TK btree.Ordered](w *writer[TK]) *Encoder[TK] {
	return &Encoder[TK]{
		jsonEncoder: json.NewEncoder(w),
		w:           w,
	}
}

// Close finalizes Update/UpdateCurrentValue by deleting any remaining old chunks.
// It is a no-op for Add/AddIfNotExist.
func (e *Encoder[TK]) Close() error {
	// Don't do anything if in add mode.
	if e.w.addOrUpdate {
		return nil
	}
	for {
		found, err := e.w.btree.Find(e.w.ctx, StreamingDataKey[TK]{Key: e.w.key, ChunkIndex: e.w.chunkIndex}, false)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		// Delete if found because the chunk belongs to the previous record that was updated.
		ok, err := e.w.btree.RemoveCurrentItem(e.w.ctx)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("update close failed, key: %v, chunk: #%d did not remove in the backend Btree", e.w.key, e.w.chunkIndex)
		}
		e.w.chunkIndex++
	}
	return nil
}

// Encode writes v as JSON to the underlying chunked writer followed by a newline.
func (e *Encoder[TK]) Encode(v any) error {
	return e.jsonEncoder.Encode(v)
}

// SetIndent controls indentation for subsequent encodes. SetIndent("", "") disables pretty printing.
func (e *Encoder[TK]) SetIndent(prefix, indent string) {
	e.jsonEncoder.SetIndent(prefix, indent)
}

// SetEscapeHTML toggles escaping of HTML characters in JSON strings.
func (e *Encoder[TK]) SetEscapeHTML(on bool) {
	e.jsonEncoder.SetEscapeHTML(on)
}
