package generate

import _ "embed"

// AboutPrompt is the system message for the about paragraph and search terms.
//
//go:embed prompt-about.md
var AboutPrompt string

// SecurityPrompt is the system message for the security note and facts.
//
//go:embed prompt-security.md
var SecurityPrompt string
