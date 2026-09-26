package detect

import _ "embed"

//go:embed corpus/libsmali.jsonl
var libsmali []byte

//go:embed corpus/libinfo.jsonl
var libinfo []byte

//go:embed corpus/exodus.json
var exodusJSON []byte
