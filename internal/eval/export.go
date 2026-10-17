package eval

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/zapstore/steroid/internal/llm"
)

// Export writes the train split as chat fine-tuning examples: the system
// prompt and user message the enricher sends, then the reference reply. A
// train case without a full reference (queries, plus an about unless the
// action is keep) is skipped and counted. dev and test cases are never
// written.
func Export(w io.Writer, cases []Case) (written, skipped int, err error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, c := range cases {
		if c.Split != "train" {
			continue
		}
		reply, ok := reference(c)
		if !ok {
			skipped++
			continue
		}
		raw, err := json.Marshal(reply)
		if err != nil {
			return written, skipped, fmt.Errorf("%s: %w", c.ID, err)
		}
		in := c.Input()
		if res := Score(c, in, reply, nil); !res.Pass() {
			return written, skipped, fmt.Errorf("%s: reference reply fails its own checks: %v", c.ID, res.Fails)
		}
		msgs := append(Messages(in), llm.Message{Role: "assistant", Content: string(raw)})
		if err := enc.Encode(map[string]any{"messages": msgs}); err != nil {
			return written, skipped, err
		}
		written++
	}
	return written, skipped, nil
}

// reference is the reply to train on. An offered description that passes
// the labels is kept unless the action is write, so the model does not
// learn to rewrite a usable one.
func reference(c Case) (Reply, bool) {
	w := c.Want
	if w.Action != "write" && candidateOK(w, c.Input().Description) {
		w.Action, w.About, w.Verdict = "keep", "", ""
	}
	if len(w.Queries) == 0 || (w.Action != "keep" && w.About == "") {
		return Reply{}, false
	}
	keywords := w.Keywords
	if keywords == nil {
		keywords = []string{}
	}
	verdict := w.Verdict
	if verdict == "" {
		verdict = "keep"
		if w.About != "" {
			verdict = "write"
		}
	}
	return Reply{Verdict: verdict, About: w.About, Keywords: keywords, Queries: w.Queries}, true
}
