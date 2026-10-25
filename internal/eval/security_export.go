package eval

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/zapstore/steroid/internal/llm"
)

// ExportSecurity writes the train split as chat fine-tuning examples: the
// system prompt and user message the enricher sends, then the reference
// reply. A train case without a reference is skipped and counted. dev and
// test cases are never written.
func ExportSecurity(w io.Writer, cases []SecurityCase) (written, skipped int, err error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, c := range cases {
		if c.Split != "train" {
			continue
		}
		reply, raw, ok := referenceSecurity(c)
		if !ok {
			skipped++
			continue
		}
		if res := ScoreSecurity(c, raw, reply, nil); !res.Pass() {
			return written, skipped, fmt.Errorf("%s: reference reply fails its own checks: %v", c.ID, res.Fails)
		}
		msgs := append(SecurityMessages(c.Input()), llm.Message{Role: "assistant", Content: raw})
		if err := enc.Encode(map[string]any{"messages": msgs}); err != nil {
			return written, skipped, err
		}
		written++
	}
	return written, skipped, nil
}
