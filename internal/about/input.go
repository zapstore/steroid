package about

import (
	"strings"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/source"
)

// Sources is everything the developer wrote about the app: the listing, the
// store texts in the repository, the README, and the website meta tags.
type Sources struct {
	Name            string
	Summary         string
	Content         string
	StoreShort      string
	StoreFull       string
	Readme          string
	SiteTitle       string
	SiteDescription string
}

// Input offers the listing description (kind 32267 content) to keep, and the
// rest of the developer's short texts as context for writing. The corpus is
// everything they wrote: a written paragraph must come from it.
func Input(app generate.App, s Sources) generate.AboutInput {
	others := Others([]Text{
		{Source: "listing summary", Body: s.Summary},
		{Source: "store short description", Body: s.StoreShort},
		{Source: "store description", Body: source.Lead(s.StoreFull)},
		{Source: "README", Body: source.Lead(s.Readme)},
		{Source: "website", Body: s.SiteDescription},
	})
	corpus := strings.Join([]string{s.Name, s.Summary, s.Content, s.StoreShort, s.StoreFull, source.Brief(s.Readme, 8000), s.SiteTitle, s.SiteDescription}, "\n")
	return generate.AboutInput{
		App:         app,
		Description: Offer(app.ID, s.Name, s.Content),
		Others:      others,
		Readme:      source.Brief(s.Readme, 2500),
		Verify:      Verify(corpus),
	}
}
