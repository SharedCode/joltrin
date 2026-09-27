package jsondb

import (
	"github.com/sharedcode/joltrin/v5/btree"
)

// IndexFieldSpecification declares a field and its sort order in the composite index.
type IndexFieldSpecification struct {
	FieldName string `json:"field_name"`
	// AscendingSortOrder chooses ascending (true) or descending (false) order.
	AscendingSortOrder bool `json:"ascending_sort_order"`
	coercedComparer    func(a, b any) int
}

// IndexSpecification defines the B-Tree key index configuration.
type IndexSpecification struct {
	// IndexFields contains the ordered list of fields participating in the index.
	IndexFields []IndexFieldSpecification `json:"index_fields"`
}

// NewIndexSpecification constructs an IndexSpecification with the provided fields.
func NewIndexSpecification(indexFields []IndexFieldSpecification) *IndexSpecification {
	return &IndexSpecification{
		IndexFields: indexFields,
	}
}

// Comparer compares two map keys using the configured field list and sort order.
func (idx *IndexSpecification) Comparer(x map[string]any, y map[string]any) int {
	for i := range idx.IndexFields {
		// Coerce the comparer once per field for efficiency.
		if idx.IndexFields[i].coercedComparer == nil {
			idx.IndexFields[i].coercedComparer = btree.CoerceComparer(x[idx.IndexFields[i].FieldName])
		}
		valX := x[idx.IndexFields[i].FieldName]
		valY := y[idx.IndexFields[i].FieldName]
		res := idx.IndexFields[i].coercedComparer(valX, valY)
		if res != 0 {
			if !idx.IndexFields[i].AscendingSortOrder {
				// Reverse the result if Descending order.
				return res * -1
			}
			return res
		}
	}
	return 0
}
