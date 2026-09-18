package main

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai/database"
)

func isExternalDocID(docID string) bool {
	trimmed := strings.TrimSpace(docID)
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "file://") || strings.HasPrefix(trimmed, "mailto:")
}

func handleViewer(w http.ResponseWriter, r *http.Request) {
	docID := r.URL.Query().Get("docID")
	if docID == "" {
		http.Error(w, "docID is required", http.StatusBadRequest)
		return
	}

	if isExternalDocID(docID) {
		writeExternalLinkInterstitial(w, docID)
		return
	}

	dbName := r.URL.Query().Get("db")
	spaceName := r.URL.Query().Get("space")

	if dbName == "" || spaceName == "" {
		http.Error(w, "db and space parameters are required", http.StatusBadRequest)
		return
	}

	dbOpt, err := getDBOptions(r.Context(), dbName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dbInstance := database.NewDatabase(dbOpt)
	tx, err := dbInstance.BeginTransaction(r.Context(), sop.ForReading)
	if err != nil {
		http.Error(w, "failed to begin transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	kb, err := dbInstance.OpenKnowledgeBase(r.Context(), spaceName, tx, nil, nil, true, false)
	if err != nil {
		http.Error(w, "failed to open knowledge base: "+err.Error(), http.StatusInternalServerError)
		return
	}

	uid, err := sop.ParseUUID(docID)
	if err != nil {
		http.Error(w, "invalid docID format", http.StatusBadRequest)
		return
	}

	docs, err := kb.Store.Documents(r.Context())
	if err != nil {
		http.Error(w, "failed to access document store: "+err.Error(), http.StatusInternalServerError)
		return
	}

	found, err := docs.Find(r.Context(), uid, false)
	if err != nil {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	if !found {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}

	documentVal, err := docs.GetCurrentValue(r.Context())
	if err != nil {
		http.Error(w, "failed to retrieve document content", http.StatusInternalServerError)
		return
	}

	if documentVal.URL != "" && documentVal.Content == "" && len(documentVal.Data) == 0 {
		redirectURL := documentVal.URL
		highlightText := r.URL.Query().Get("text")

		if highlightText != "" && !strings.Contains(redirectURL, "#") {
			// Limit text to a reasonable length for the browser URI
			if len(highlightText) > 60 {
				highlightText = highlightText[:60]
			}
			encodedText := url.QueryEscape(highlightText)
			encodedText = strings.ReplaceAll(encodedText, "+", "%20")
			redirectURL += "#:~:text=" + encodedText
		}

		writeExternalLinkInterstitial(w, redirectURL)
		return
	}

	if documentVal.ContentType != "" {
		w.Header().Set("Content-Type", documentVal.ContentType)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}

	w.WriteHeader(http.StatusOK)
	if len(documentVal.Data) > 0 {
		w.Write(documentVal.Data)
	} else {
		w.Write([]byte(documentVal.Content))
	}
}
