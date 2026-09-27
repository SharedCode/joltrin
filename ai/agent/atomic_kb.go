package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// invokeLoopbackAPI routes a synthetic HTTP request to the main server's DefaultServeMux.
// This ensures that the AST LLM tools use the EXACT same logic, transactions, and handlers
// as the UI/REST API. No duplicate code.
func invokeLoopbackAPI(ctx context.Context, method, apiPath string, query url.Values, body map[string]any) (any, error) {
	var reqBody []byte
	var err error
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	targetURL := apiPath
	if len(query) > 0 {
		targetURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()

	// Delegate directly to the global HTTP multiplexer where tools/httpserver/main.go binds the UI handlers
	http.DefaultServeMux.ServeHTTP(rec, req)

	if rec.Code >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}

	var responseData any
	if err := json.Unmarshal(rec.Body.Bytes(), &responseData); err != nil {
		return rec.Body.String(), nil
	}
	return responseData, nil
}

func (e *ScriptEngine) ExecuteKBManagement(ctx context.Context, op string, args map[string]any, input any) (any, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return nil, fmt.Errorf("no active session payload context to execute KB management")
	}

	kbName := "sop"
	if p.AgentID != "omni" && p.AvatarScope != "" {
		kbName = p.AvatarScope
	} else if len(p.SelectedKBs) > 0 {
		kbName = p.SelectedKBs[0].Name
	}

	limit := "100"
	if l, ok := args["limit"].(float64); ok {
		limit = fmt.Sprintf("%.0f", l)
	}

	offset := "0"
	if o, ok := args["offset"].(float64); ok {
		offset = fmt.Sprintf("%.0f", o)
	}

	query := url.Values{}
	query.Set("name", kbName)
	query.Set("database", p.CurrentDB)

	// Helper function for quick category path to UUID resolution
	resolveCatToUUID := func(catStr string) string {
		if catStr == "" {
			return ""
		}
		sysDbIntf, err := e.ResolveDatabase("system")
		if err == nil {
			if sysDB, ok := sysDbIntf.(*database.Database); ok {
				if tx, err := sysDB.BeginTransaction(ctx, sop.ForReading); err == nil {
					defer tx.Rollback(ctx)
					if kb, err := sysDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false); err == nil {
						if pathTree, err := kb.Store.CategoriesByPath(ctx); err == nil {
							if found, _ := pathTree.Find(ctx, catStr, false); found {
								if uuidVal, err := pathTree.GetCurrentValue(ctx); err == nil {
									return uuidVal.String()
								}
							}
						}
					}
				}
			}
		}
		return catStr // fallback
	}

	switch op {
	case "search_space":
		userDbIntf, err := e.ResolveDatabase(p.CurrentDB)
		if err != nil {
			return nil, err
		}
		userDB := userDbIntf.(*database.Database)
		tx, err := userDB.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		kb, err := userDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false)
		if err != nil {
			return nil, err
		}
		queryStr, _ := args["query"].(string)
		if queryStr == "" {
			if v, ok := args["search_text"].(string); ok {
				queryStr = v
			}
		}
		catPathStr := ""
		if cp, ok := args["category_path"].(string); ok {
			catPathStr = cp
		} else if pt, ok := args["path"].(string); ok {
			catPathStr = pt
		}

		if catPathStr != "" {
			pathHits, err := kb.SearchByPath(ctx, []memory.PathSearchParam{{CategoryPath: catPathStr, SearchText: queryStr}})
			if err != nil {
				return nil, err
			}
			var cleanHits []map[string]any
			for _, h := range pathHits {
				catStr := ""
				text := ""
				if h.Data != nil {
					if catVal, ok := h.Data["category"].(string); ok {
						catStr = catVal
					}
					if txt, ok := h.Data["text"].(string); ok {
						text = txt
					} else if d, ok := h.Data["description"].(string); ok {
						text = d
					}
				}
				if text == "" && len(h.Summaries) > 0 {
					text = h.Summaries[0]
				}
				cleanHits = append(cleanHits, map[string]any{
					"category": catStr,
					"content":  text,
					"doc_id":   h.DocID,
				})
			}
			return map[string]any{"data": cleanHits, "total": len(cleanHits)}, nil
		}

		// Fallback: Invoke standard API search behavior if no path provided
		var mappedPayload map[string]any
		if queryStr != "" {
			mappedPayload = map[string]any{"query": queryStr}
		}
		return invokeLoopbackAPI(ctx, "GET", "/api/store/search", query, mappedPayload)

	case "list_space_categories":
		userDbIntf, err := e.ResolveDatabase(p.CurrentDB)
		if err != nil {
			return nil, err
		}
		userDB := userDbIntf.(*database.Database)
		tx, err := userDB.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		kb, err := userDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false)
		if err != nil {
			return nil, err
		}
		limitVal := 100
		offsetVal := 0
		fmt.Sscanf(limit, "%d", &limitVal)
		fmt.Sscanf(offset, "%d", &offsetVal)

		cats, total, err := kb.ListCategories(ctx, memory.ListCategoriesParam{Limit: limitVal, Offset: offsetVal})
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": cats, "total": total}, nil

	case "list_space_items":
		userDbIntf, err := e.ResolveDatabase(p.CurrentDB)
		if err != nil {
			return nil, err
		}
		userDB := userDbIntf.(*database.Database)
		tx, err := userDB.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		kb, err := userDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false)
		if err != nil {
			return nil, err
		}
		limitVal := 100
		offsetVal := 0
		fmt.Sscanf(limit, "%d", &limitVal)
		fmt.Sscanf(offset, "%d", &offsetVal)

		catFilter := ""
		if cat, _ := args["category"].(string); cat != "" {
			catFilter = cat
		}

		items, total, err := kb.ListItems(ctx, memory.ListItemsParam{CategoryPath: catFilter, Limit: limitVal, Offset: offsetVal})
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": items, "total": total}, nil

	case "upsert_space_items":
		var finalResults []any
		if itemsRaw, ok := args["items"].([]any); ok {
			for _, itemRaw := range itemsRaw {
				if itemMap, ok := itemRaw.(map[string]any); ok {
					catName, _ := itemMap["category"].(string)
					itemName, _ := itemMap["item_name"].(string)
					content, _ := itemMap["content"].(string)

					mappedPayload := map[string]any{
						"category_id": resolveCatToUUID(catName),
						"data": map[string]any{
							"name":    itemName,
							"content": content,
						},
						"summaries": []string{content},
					}
					res, err := invokeLoopbackAPI(ctx, "POST", "/api/spaces/item/add", query, mappedPayload)
					if err == nil {
						finalResults = append(finalResults, res)
					}
				}
			}
		}
		return finalResults, nil

	case "delete_space_categories":
		userDbIntf, err := e.ResolveDatabase(p.CurrentDB)
		if err != nil {
			return nil, err
		}
		userDB := userDbIntf.(*database.Database)
		tx, err := userDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		kb, err := userDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false)
		if err != nil {
			return nil, err
		}

		var finalResults []any
		if catsRaw, ok := args["categories"].([]any); ok {
			for _, catRaw := range catsRaw {
				catStr, _ := catRaw.(string)
				catID := resolveCatToUUID(catStr)
				parsedID, errP := sop.ParseUUID(catID)
				if errP == nil {
					if err := kb.DeleteCategories(ctx, []sop.UUID{parsedID}); err == nil {
						finalResults = append(finalResults, map[string]any{"category": catStr, "status": "deleted"})
					}
				}
			}
		}
		tx.Commit(ctx)
		return finalResults, nil

	case "delete_space_items":
		userDbIntf, err := e.ResolveDatabase(p.CurrentDB)
		if err != nil {
			return nil, err
		}
		userDB := userDbIntf.(*database.Database)
		tx, err := userDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		kb, err := userDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false)
		if err != nil {
			return nil, err
		}

		var itemKeys []memory.ItemKey
		var finalResults []any

		if itemsRaw, ok := args["items"].([]any); ok {
			itemsTree, err := kb.Store.Items(ctx)
			if err == nil && itemsTree != nil {
				for _, itemRaw := range itemsRaw {
					if itemMap, ok := itemRaw.(map[string]any); ok {
						catStr, _ := itemMap["category"].(string)
						itemName, _ := itemMap["item_name"].(string)

						resolvedCat := resolveCatToUUID(catStr)
						catUUID, errP := sop.ParseUUID(resolvedCat)
						if errP == nil {
							if ok, err := itemsTree.First(ctx); ok && err == nil {
								for ok {
									k := itemsTree.GetCurrentKey().Key
									if k.CategoryID == catUUID {
										v, err := itemsTree.GetCurrentValue(ctx)
										if err == nil {
											dataName := ""
											if dataMap, ok := any(v.Data).(map[string]any); ok {
												if n, ok := dataMap["name"].(string); ok {
													dataName = n
												}
											}
											if dataName == itemName {
												itemKeys = append(itemKeys, k)
												finalResults = append(finalResults, map[string]any{"category": catStr, "item_name": itemName, "status": "deleted"})
												break
											}
										}
									}
									ok, _ = itemsTree.Next(ctx)
								}
							}
						}
					}
				}
			}
		}

		if len(itemKeys) > 0 {
			if err := kb.DeleteItems(ctx, itemKeys); err != nil {
				return nil, err
			}
		}
		tx.Commit(ctx)
		return finalResults, nil

	case "vectorize_space":
		return invokeLoopbackAPI(ctx, "POST", "/api/spaces/vectorize", query, nil)

	case "vectorize_space_categories":
		var results []any
		if cats, ok := args["categories"].([]any); ok {
			for _, c := range cats {
				if cStr, ok := c.(string); ok && cStr != "" {
					resolvedId := resolveCatToUUID(cStr)
					reqBody := map[string]any{"space": kbName, "database": p.CurrentDB, "categoryId": resolvedId}
					res, _ := invokeLoopbackAPI(ctx, "POST", "/api/spaces/vectorize", nil, reqBody)
					results = append(results, res)
				}
			}
		}
		return results, nil

	case "vectorize_space_items":
		catStr, _ := args["category"].(string)
		resolvedCatId := resolveCatToUUID(catStr)
		catUUID, err := sop.ParseUUID(resolvedCatId)
		if err != nil {
			return nil, fmt.Errorf("invalid category to vectorize items: %v", err)
		}

		var itemUUIDs []string
		if itemNames, ok := args["item_names"].([]any); ok {
			userDbIntf, err := e.ResolveDatabase(p.CurrentDB)
			if err == nil {
				userDB := userDbIntf.(*database.Database)
				if tx, err := userDB.BeginTransaction(ctx, sop.ForReading); err == nil {
					if kb, err := userDB.OpenKnowledgeBase(ctx, kbName, tx, nil, nil, false, false); err == nil {
						if itemsTree, err := kb.Store.Items(ctx); err == nil {
							if ok, err := itemsTree.First(ctx); ok && err == nil {
								for ok {
									k := itemsTree.GetCurrentKey().Key
									if k.CategoryID == catUUID {
										v, err := itemsTree.GetCurrentValue(ctx)
										if err == nil {
											dataName := ""
											if dataMap, ok := any(v.Data).(map[string]any); ok {
												if n, ok := dataMap["name"].(string); ok {
													dataName = n
												}
											}
											for _, n := range itemNames {
												if nStr, strOk := n.(string); strOk && nStr == dataName {
													itemUUIDs = append(itemUUIDs, k.ItemID.String())
												}
											}
										}
									}
									ok, _ = itemsTree.Next(ctx)
								}
							}
						}
					}
					tx.Rollback(ctx)
				}
			}
		}
		reqBody := map[string]any{"space": kbName, "database": p.CurrentDB, "categoryId": resolvedCatId, "itemIds": itemUUIDs}
		return invokeLoopbackAPI(ctx, "POST", "/api/spaces/vectorize", nil, reqBody)

	default:
		return nil, fmt.Errorf("unsupported KB management operation: %s", op)
	}
}
