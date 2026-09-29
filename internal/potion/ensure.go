package potion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var fetchClient = &http.Client{Timeout: 5 * time.Minute}

type pinnedFile struct {
	name string
	url  string
	sum  string
}

var modelFiles = []pinnedFile{
	{name: "tokenizer.json", url: hfRoot + "tokenizer.json", sum: "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45"},
	{name: "model.safetensors", url: hfRoot + "model.safetensors", sum: "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c"},
}

func ensure(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range modelFiles {
		if err := ensureFile(ctx, filepath.Join(dir, f.name), f.url, f.sum); err != nil {
			return err
		}
	}
	return nil
}

func ensureFile(ctx context.Context, path, url, sum string) error {
	ok, err := fileSumOK(path, sum)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	tmp := path + ".tmp"
	if err := download(ctx, url, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	ok, err = fileSumOK(tmp, sum)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if !ok {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: sha256 mismatch", filepath.Base(path))
	}
	return os.Rename(tmp, path)
}

func fileSumOK(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

func download(ctx context.Context, rawURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "steroid")
	res, err := fetchClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, res.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
