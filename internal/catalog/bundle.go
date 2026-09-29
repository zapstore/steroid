package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildBundle writes a client bundle for from→to. Artifact bytes are the latest files.
// about, security, and facts are left out when a bundle that ended at from already carried the same bytes.
// The client keeps the stored text for a missing member.
func BuildBundle(ctx context.Context, data string, from, to, sealedAt int64, diff Diff, signer Signer) ([]byte, error) {
	raw, err := MarshalDiff(diff)
	if err != nil {
		return nil, err
	}
	prior := bundlesEndingAt(data, from)
	var members []member
	if len(raw) > 0 {
		members = append(members, member{Name: "diff.jsonl", Data: raw})
	}
	for _, appID := range diff.Apps {
		if appID == "" || strings.Contains(appID, "/") || strings.Contains(appID, "..") {
			return nil, fmt.Errorf("app id %q", appID)
		}
		dir := AppDir(data, appID)
		for _, name := range []string{"about", "security", "facts", "vector", "icon.webp"} {
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			if len(body) == 0 {
				continue
			}
			memberName := filepath.ToSlash(filepath.Join(appID, name))
			if noteFile(name) && sameNote(prior, memberName, body) {
				continue
			}
			members = append(members, member{Name: memberName, Data: body})
		}
	}
	for _, name := range diff.Avatars {
		if !isAvatarFilename(name) {
			return nil, fmt.Errorf("avatar name %q", name)
		}
		body, err := os.ReadFile(filepath.Join(ArtifactDir(data), name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		members = append(members, member{Name: name, Data: body})
	}
	return pack(ctx, members, from, to, sealedAt, &signer)
}

func noteFile(name string) bool {
	switch name {
	case "about", "security", "facts":
		return true
	default:
		return false
	}
}

// sameNote reports that some bundle ending at the previous epoch already shipped these bytes.
func sameNote(prior []map[string][]byte, name string, body []byte) bool {
	for _, files := range prior {
		if prev, ok := files[name]; ok && bytes.Equal(prev, body) {
			return true
		}
	}
	return false
}

func bundlesEndingAt(data string, epoch int64) []map[string][]byte {
	if epoch <= 0 {
		return nil
	}
	entries, err := os.ReadDir(BundleDir(data))
	if err != nil {
		return nil
	}
	tail := fmt.Sprintf("-%d.tar.zst", epoch)
	var out []map[string][]byte
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), tail) {
			continue
		}
		files, err := unpack(filepath.Join(BundleDir(data), entry.Name()))
		if err != nil {
			continue
		}
		out = append(out, files)
	}
	return out
}
