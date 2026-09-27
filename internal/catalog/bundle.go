package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildBundle writes a client bundle for from→to. Artifact bytes are the latest files.
func BuildBundle(ctx context.Context, data string, from, to, sealedAt int64, diff Diff, signer Signer) ([]byte, error) {
	raw, err := MarshalDiff(diff)
	if err != nil {
		return nil, err
	}
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
			members = append(members, member{
				Name: filepath.ToSlash(filepath.Join(appID, name)),
				Data: body,
			})
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
