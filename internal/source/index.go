package source

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/philippgille/chromem-go"
)

const (
	maxIndexChunks      = 480
	maxOutboundChunks   = 16
	minSourceSimilarity = 0.35
)

// One window of source. chromem ranks these for every fact the tree can show.
type indexedChunk struct {
	path  string
	lines []hit
}

const (
	offlineQuery = "where the screen loads what the person sees, from a local database or files, or from a network client, and work that runs only when a network is available"
	accountQuery = "a sign-in that blocks the main screen, a login that can be skipped, or a key stored on the device"
	encryptQuery = "user content encrypted so the server cannot read it"
)

var outboundQueries = []string{
	exfilQuery,
	"http client websocket or sync sending data to a remote host",
	"crash reporter analytics or tracker request leaving the device",
}

func appendChunks(chunks []indexedChunk, rel string, lines []string) []indexedChunk {
	if len(chunks) >= maxIndexChunks || skipIndex(rel) || !codeExt(rel) {
		return chunks
	}
	found := false
	for i, line := range lines {
		if !funcHeader(line) {
			continue
		}
		_, end, ok := functionBounds(lines, i)
		if !ok {
			continue
		}
		chunks = append(chunks, chunkWindow(rel, lines, i, end))
		found = true
		if len(chunks) >= maxIndexChunks {
			return chunks
		}
	}
	if found || len(lines) == 0 {
		return chunks
	}
	end := len(lines) - 1
	if end > 48 {
		end = 48
	}
	return append(chunks, chunkWindow(rel, lines, 0, end))
}

func chunkWindow(rel string, lines []string, start, end int) indexedChunk {
	var out []hit
	for i := start; i <= end && i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if !codeLine(text) {
			continue
		}
		out = append(out, hit{path: rel, line: i + 1, text: clipRunes(text, maxLineRunes)})
	}
	return indexedChunk{path: rel, lines: out}
}

func skipIndex(rel string) bool {
	if isTestPath(rel) {
		return true
	}
	for _, part := range strings.Split(strings.ToLower(rel), "/") {
		switch part {
		case "node_modules", "vendor", "build", "dist", ".gradle", "generated":
			return true
		}
	}
	return false
}

func chunkText(c indexedChunk) string {
	var b strings.Builder
	b.WriteString(c.path)
	b.WriteByte('\n')
	for _, line := range c.lines {
		b.WriteString(line.text)
		b.WriteByte('\n')
	}
	return b.String()
}

// applySourceIndex asks one index where each source fact is answered.
// A hit replaces the phrase quote. A miss leaves the phrase quote in place.
func applySourceIndex(ctx context.Context, embed Embedder, chunks []indexedChunk, asks []Use, uses map[string]quote, outbound []hit, account, encrypt, offline *quote) ([]hit, *quote, *quote, *quote) {
	if embed == nil || len(chunks) == 0 {
		return outbound, account, encrypt, offline
	}
	col, ok := indexChunks(ctx, embed, chunks)
	if !ok {
		return outbound, account, encrypt, offline
	}
	if q, ok := topChunk(ctx, col, embed, chunks, offlineQuery); ok {
		offline = &q
	}
	if q, ok := topChunk(ctx, col, embed, chunks, accountQuery); ok {
		account = &q
	}
	if q, ok := topChunk(ctx, col, embed, chunks, encryptQuery); ok {
		encrypt = &q
	}
	for _, ask := range asks {
		qtext := "where " + strings.ReplaceAll(ask.Fact, "_", " ") + " data is read, and whether it stays on the phone or is sent"
		q, ok := topChunk(ctx, col, embed, chunks, qtext)
		if !ok {
			continue
		}
		q.score = 6
		uses[ask.Fact] = q
	}
	if ranked := rankChunks(ctx, col, chunks, outboundQueries, maxOutboundChunks); len(ranked) > 0 {
		var lines []hit
		for _, q := range ranked {
			lines = append(lines, q.lines...)
		}
		outbound = lines
	}
	return outbound, account, encrypt, offline
}

func rankChunks(ctx context.Context, col *chromem.Collection, chunks []indexedChunk, queries []string, limit int) []quote {
	if col == nil || limit < 1 {
		return nil
	}
	n := col.Count()
	want := 12
	if n < want {
		want = n
	}
	if want < 1 {
		return nil
	}
	best := map[int]float32{}
	for _, query := range queries {
		hits, err := col.Query(ctx, query, want, nil, nil)
		if err != nil {
			continue
		}
		for _, h := range hits {
			if h.Similarity < minSourceSimilarity {
				continue
			}
			i, err := strconv.Atoi(h.ID)
			if err != nil || i < 0 || i >= len(chunks) || len(chunks[i].lines) == 0 {
				continue
			}
			if prev, ok := best[i]; ok && prev >= h.Similarity {
				continue
			}
			best[i] = h.Similarity
		}
	}
	type item struct {
		i   int
		sim float32
	}
	var items []item
	for i, sim := range best {
		items = append(items, item{i, sim})
	}
	sort.Slice(items, func(a, b int) bool {
		return items[a].sim > items[b].sim
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]quote, 0, len(items))
	for _, it := range items {
		out = append(out, quote{path: chunks[it.i].path, lines: chunks[it.i].lines})
	}
	return out
}

func indexChunks(ctx context.Context, embed Embedder, chunks []indexedChunk) (*chromem.Collection, bool) {
	db := chromem.NewDB()
	col, err := db.CreateCollection("source", nil, chromem.EmbeddingFunc(embed))
	if err != nil {
		return nil, false
	}
	docs := make([]chromem.Document, 0, len(chunks))
	for i, chunk := range chunks {
		if len(chunk.lines) == 0 {
			continue
		}
		docs = append(docs, chromem.Document{
			ID:      strconv.Itoa(i),
			Content: chunkText(chunk),
		})
	}
	if len(docs) == 0 {
		return nil, false
	}
	if err := col.AddDocuments(ctx, docs, 1); err != nil {
		return nil, false
	}
	return col, true
}

func topChunk(ctx context.Context, col *chromem.Collection, embed Embedder, chunks []indexedChunk, query string) (quote, bool) {
	return topChunkSim(ctx, col, embed, chunks, query, minSourceSimilarity)
}

func topChunkSim(ctx context.Context, col *chromem.Collection, _ Embedder, chunks []indexedChunk, query string, minSim float32) (quote, bool) {
	n := col.Count()
	if n > 4 {
		n = 4
	}
	if n < 1 {
		return quote{}, false
	}
	hits, err := col.Query(ctx, query, n, nil, nil)
	if err != nil || len(hits) == 0 || hits[0].Similarity < minSim {
		return quote{}, false
	}
	i, err := strconv.Atoi(hits[0].ID)
	if err != nil || i < 0 || i >= len(chunks) || len(chunks[i].lines) == 0 {
		return quote{}, false
	}
	return quote{path: chunks[i].path, lines: chunks[i].lines}, true
}
