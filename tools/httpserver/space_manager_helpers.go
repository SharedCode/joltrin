package main

func buildChunkData(cid string, chunk SpaceIngestChunk, documentMode bool) map[string]any {
	if len(chunk.Data) > 0 {
		if _, exists := chunk.Data["original_id"]; !exists && cid != "" {
			chunk.Data["original_id"] = cid
		}
		if len(chunk.DocID) > 0 {
			chunk.Data["doc_id"] = chunk.DocID
		}
		return chunk.Data
	}

	text := chunk.Text
	desc := chunk.Description

	// In DocumentMode=true, Text and Description act only as index/search summaries,
	// because the canonical data is external to the item. Thus, we truncate them
	// to keep payloads optimal. In DocumentMode=false, the item itself is the canonical
	// data source, so we do not restrict text length.
	if documentMode {
		if len(text) > 800 {
			runes := []rune(text)
			if len(runes) > 800 {
				text = string(runes[:800]) + "... (truncated)"
			}
		}

		if len(desc) > 800 {
			runes := []rune(desc)
			if len(runes) > 800 {
				desc = string(runes[:800]) + "... (truncated)"
			}
		}
	}

	res := map[string]any{
		"text":        text,
		"description": desc,
		"category":    chunk.Category,
		"original_id": cid,
	}

	if len(chunk.DocID) > 0 {
		res["doc_id"] = chunk.DocID
	}

	return res
}
