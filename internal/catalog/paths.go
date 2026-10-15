package catalog

import "fmt"

// avatarFilename is <64 hex characters>.webp.
func avatarFilename(pubkey string) (string, error) {
	if len(pubkey) != 64 {
		return "", fmt.Errorf("avatar pubkey")
	}
	for _, c := range pubkey {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return "", fmt.Errorf("avatar pubkey")
		}
	}
	return pubkey + ".webp", nil
}
