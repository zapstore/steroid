// Package doc renders scanner rows as the fact sheet stored for an app.
package doc

import (
	"strings"

	"github.com/zapstore/steroid/internal/scan"
)

// Facts renders scanner rows as "fact: value" lines, in scanner order.
func Facts(rows []scan.Row) string {
	var b strings.Builder
	for _, row := range rows {
		if row.Fact == "" || (row.Value != "yes" && row.Value != "no") {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(row.Fact)
		b.WriteString(": ")
		b.WriteString(row.Value)
	}
	return b.String()
}
