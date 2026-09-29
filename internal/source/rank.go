package source

import (
	"context"
	"strconv"
	"strings"

	"github.com/philippgille/chromem-go"
)

// Embedder is the static code model chromem uses to rank windows.
// Nil keeps keyword order. This is not the leaf catalog model.
type Embedder func(ctx context.Context, text string) ([]float32, error)

// Asked of every outbound window. The nearest windows lead the Outbound section.
const exfilQuery = "personal data sent off the phone in a request: location, contacts, recordings, messages, clipboard, identifiers, tokens"

func rankOutbound(ctx context.Context, groups []outboundGroup, embed Embedder) ([]outboundGroup, bool) {
	if embed == nil || len(groups) < 2 {
		return nil, false
	}
	db := chromem.NewDB()
	col, err := db.CreateCollection("outbound", nil, chromem.EmbeddingFunc(embed))
	if err != nil {
		return nil, false
	}
	docs := make([]chromem.Document, 0, len(groups))
	for i, g := range groups {
		docs = append(docs, chromem.Document{
			ID:      strconv.Itoa(i),
			Content: groupText(g),
		})
	}
	if err := col.AddDocuments(ctx, docs, 1); err != nil {
		return nil, false
	}
	hits, err := col.Query(ctx, exfilQuery, len(docs), nil, nil)
	if err != nil || len(hits) != len(groups) {
		return nil, false
	}
	ordered := make([]outboundGroup, 0, len(groups))
	seen := make([]bool, len(groups))
	for _, hit := range hits {
		i, err := strconv.Atoi(hit.ID)
		if err != nil || i < 0 || i >= len(groups) || seen[i] {
			return nil, false
		}
		seen[i] = true
		ordered = append(ordered, groups[i])
	}
	return ordered, true
}

func groupText(g outboundGroup) string {
	var b strings.Builder
	b.WriteString(g.path)
	b.WriteByte('\n')
	for _, line := range g.lines {
		b.WriteString(line.text)
		b.WriteByte('\n')
	}
	return b.String()
}
