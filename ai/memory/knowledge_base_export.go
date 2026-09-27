package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// ExportData	defines	the	structure	of	the	KnowledgeBase	JSON	payload.
type ExportData[T any] struct {
	Config     *KnowledgeBaseConfig `json:"config,omitempty"`
	Categories []*Category          `json:"categories"`
	Documents  []*Document          `json:"documents,omitempty"`
	Items      []ExportItem[T]      `json:"items"`
}

// ExportItem	dictates	what	fields	from	the	item	are	serialized.
type ExportItem[T any] struct {
	CategoryPath     string      `json:"category"`
	DocID            DocIDs      `json:"doc_id,omitempty"`
	Data             T           `json:"data"`
	Summaries        []string    `json:"summaries,omitempty"`
	SummariesVectors [][]float32 `json:"summaries_vectors,omitempty"`
	Positions        []VectorKey `json:"positions,omitempty"`
	VectorHash       string      `json:"vector_hash,omitempty"`
}

// ExportJSON	serializes	the	KnowledgeBase	contents	into	a	JSON	stream.
func (kb *KnowledgeBase[T]) ExportJSON(ctx context.Context, writer io.Writer) error {
	catBtree, err := kb.Store.Categories(ctx)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(writer)
	io.WriteString(writer, "{\n")

	//	1.	Config	block
	itemsBtree, err := kb.Store.Items(ctx)
	if err != nil {
		return err
	}
	hasConfig := false
	ok, _ := itemsBtree.First(ctx)
	for ok {
		item, err := itemsBtree.GetCurrentValue(ctx)
		if err == nil && item.ID == sop.NilUUID {
			b, err := json.Marshal(item.Data)
			if err == nil {
				var cfg KnowledgeBaseConfig
				if json.Unmarshal(b, &cfg) == nil {
					io.WriteString(writer, "\"config\":	")
					encoder.Encode(cfg)
					hasConfig = true
				}
			}
			break
		}
		ok, _ = itemsBtree.Next(ctx)
	}

	if hasConfig {
		io.WriteString(writer, ",\n")
	}

	//	2.	Categories	array
	io.WriteString(writer, "\"categories\":	[\n")
	catMap := make(map[sop.UUID]string)
	firstCat := true
	ok, _ = catBtree.First(ctx)
	for ok {
		cat, err := catBtree.GetCurrentValue(ctx)
		if err == nil && cat != nil {
			if !firstCat {
				io.WriteString(writer, ",\n")
			}
			firstCat = false
			encoder.Encode(cat)
			catMap[cat.ID] = cat.Name
		}
		ok, _ = catBtree.Next(ctx)
	}
	io.WriteString(writer, "\n]")

	//	3.	Items	array
	io.WriteString(writer, ",\n\"items\":	[\n")
	firstItem := true
	ok, _ = itemsBtree.First(ctx)
	for ok {
		item, err := itemsBtree.GetCurrentValue(ctx)
		if err == nil && item.ID != sop.NilUUID {
			var vectors [][]float32
			if len(item.Positions) > 0 {
				vecBtree, _ := kb.Store.Vectors(ctx)
				if vecBtree != nil {
					for _, pos := range item.Positions {
						has, _ := vecBtree.Find(ctx, pos, false)
						if has {
							v, _ := vecBtree.GetCurrentValue(ctx)
							vectors = append(vectors, v.Data)
						}
					}
				}
			}

			catName := catMap[item.CategoryID]
			if !firstItem {
				io.WriteString(writer, ",\n")
			}
			firstItem = false
			encoder.Encode(ExportItem[T]{
				CategoryPath:     catName,
				DocID:            item.DocID,
				Data:             item.Data,
				Summaries:        item.Summaries,
				SummariesVectors: vectors,
				Positions:        item.Positions,
				VectorHash:       item.VectorHash,
			})
		}
		ok, _ = itemsBtree.Next(ctx)
	}

	io.WriteString(writer, "\n]\n}\n")
	return nil
}

// ImportJSON	deserializes	a	JSON	stream	and	ingests	it	into	the	KnowledgeBase.
func (kb *KnowledgeBase[T]) ImportJSON(ctx context.Context, reader io.Reader, persona string, onEnrich ...func(*ExportItem[T])) error {
	decoder := json.NewDecoder(reader)

	//	Read	opening	'{'
	t, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected	'{'	at	the	beginning	of	stream")
	}

	catBtree, err := kb.Store.Categories(ctx)
	if err != nil {
		return err
	}
	catsByPath, err := kb.Store.CategoriesByPath(ctx)
	if err != nil {
		return err
	}
	uuidMap := make(map[sop.UUID]sop.UUID)

	hasMissingVectors := false

	for decoder.More() {
		t, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return fmt.Errorf("expected	string	key")
		}

		if key == "config" {
			var configData KnowledgeBaseConfig
			if err := decoder.Decode(&configData); err != nil {
				return fmt.Errorf("decode kb config: %w", err)
			}
			configData.Description = strings.TrimSpace(configData.Description)
			if err := kb.SetConfig(ctx, &configData); err != nil {
				return fmt.Errorf("persist kb config: %w", err)
			}
			continue
		}
		if key == "categories" {
			//	Read	opening	'['
			if _, err := decoder.Token(); err != nil { //	'['
				return err
			}
			for decoder.More() {
				var c Category
				if err := decoder.Decode(&c); err != nil {
					return err
				}
				if len(c.CenterVector) == 0 {
					hasMissingVectors = true
				}
				//	Deduplicate	based	on	Path	instead	of	Name
				ok, _ := catBtree.First(ctx)
				found := false
				for ok {
					existing, _ := catBtree.GetCurrentValue(ctx)
					if existing != nil && existing.Path == c.Path && c.Path != "" {
						uuidMap[c.ID] = existing.ID
						found = true
						break
					} else if existing != nil && existing.Path == "" && existing.Name == c.Name {
						uuidMap[c.ID] = existing.ID
						found = true
						break
					}
					ok, _ = catBtree.Next(ctx)
				}
				if !found {
					oldID := c.ID
					if c.ID.IsNil() {
						c.ID = sop.NewUUID()
					}
					uuidMap[oldID] = c.ID
					for i := range c.ParentIDs {
						if mappedID, ok := uuidMap[c.ParentIDs[i].ParentID]; ok {
							c.ParentIDs[i].ParentID = mappedID
						}
					}
					catBtree.Add(ctx, c.ID, &c)
					path := c.Path
					if path == "" {
						path = c.Name
					}
					if path != "" {
						catsByPath.Add(ctx, path, c.ID)
					}
				}
			} // Rebuild Bi-Directional Children Links
			updates := make(map[sop.UUID][]sop.UUID)
			okCat, _ := catBtree.First(ctx)
			for okCat {
				c, _ := catBtree.GetCurrentValue(ctx)
				if c != nil {
					for _, p := range c.ParentIDs {
						updates[p.ParentID] = append(updates[p.ParentID], c.ID)
					}
				}
				okCat, _ = catBtree.Next(ctx)
			}
			for parentID, children := range updates {
				if found, _ := catBtree.Find(ctx, parentID, false); found {
					parent, _ := catBtree.GetCurrentValue(ctx)
					if parent != nil {
						for _, newChild := range children {
							exists := false
							for _, existingChild := range parent.ChildrenIDs {
								if existingChild == newChild {
									exists = true
									break
								}
							}
							if !exists {
								parent.ChildrenIDs = append(parent.ChildrenIDs, newChild)
							}
						}
						catBtree.UpdateCurrentItem(ctx, parentID, parent)
					}
				}
			}
			if _, err := decoder.Token(); err != nil { // Read closing ']'
				return err
			}
		} else if key == "documents" {
			// Read opening '['
			if _, err := decoder.Token(); err != nil { // '['
				return err
			}
			for decoder.More() {
				var doc Document
				if err := decoder.Decode(&doc); err != nil {
					return err
				}
				if doc.ID.IsNil() {
					doc.ID = sop.NewUUID()
				}
				if err := kb.Store.UpsertDocument(ctx, doc); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil { // Read closing ']'
				return err
			}
		} else if key == "items" {
			//	Read	opening	'['
			if _, err := decoder.Token(); err != nil { //	'['
				return err
			}
			var thoughts []Thought[T]
			for decoder.More() {
				var it ExportItem[T]
				if err := decoder.Decode(&it); err != nil {
					return err
				}

				if len(it.SummariesVectors) == 0 {
					hasMissingVectors = true
				}

				for _, enrich := range onEnrich {
					if enrich != nil {
						enrich(&it)
					}
				}

				// If DocumentMode is true, we truncate the generic text/description metadata
				// after enrichment (so embeddings/summaries are generated fairly), but before storage.
				if cfg, err := kb.GetConfig(ctx); err == nil && cfg != nil && cfg.DocumentMode {
					if dataMap, ok := any(it.Data).(map[string]any); ok {
						if txt, ok := dataMap["text"].(string); ok && len(txt) > 800 {
							runes := []rune(txt)
							if len(runes) > 800 {
								dataMap["text"] = string(runes[:800]) + "... (truncated)"
							}
						}
						if desc, ok := dataMap["description"].(string); ok && len(desc) > 800 {
							runes := []rune(desc)
							if len(runes) > 800 {
								dataMap["description"] = string(runes[:800]) + "... (truncated)"
							}
						}
						it.Data = any(dataMap).(T)
					}
				}

				if parsedID, err := sop.ParseUUID(it.CategoryPath); err == nil {
					if mapped, ok := uuidMap[parsedID]; ok {
						it.CategoryPath = mapped.String()
					}
				}
				thoughts = append(thoughts, Thought[T]{
					CategoryPath: it.CategoryPath,
					DocID:        it.DocID,
					Data:         it.Data,
					Vectors:      it.SummariesVectors,
					Summaries:    it.Summaries,
					Positions:    it.Positions,
					VectorHash:   it.VectorHash,
				})

				//	Submit	in	batches	to	keep	memory	usage	low
				if len(thoughts) >= 500 {
					if err := kb.IngestThoughts(ctx, thoughts, persona); err != nil {
						return err
					}
					thoughts = thoughts[:0] //	Keep	capacity,	reset	length
				}
			}
			if _, err := decoder.Token(); err != nil { //	Read	closing	']'
				return err
			}

			//	Submit	remaining	tail
			if len(thoughts) > 0 {
				if err := kb.IngestThoughts(ctx, thoughts, persona); err != nil {
					return err
				}
			}
		} else {
			//	Ignore	any	unknown	root	keys
			var discard any
			decoder.Decode(&discard)
		}
	}

	if _, err := decoder.Token(); err != nil { //	Read	closing	'}'
		return err
	}
	if cfg, err := kb.GetConfig(ctx); err == nil && cfg != nil {
		now := time.Now().Unix()
		cfg.LastModified = now
		if !hasMissingVectors {
			cfg.LastVectorized = now
		}
		kb.SetConfig(ctx, cfg)
	}
	return nil
}
