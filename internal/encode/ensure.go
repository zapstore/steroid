package encode

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	modelRev = "4262131b32c3182bd06e67e92ae69d7bd66e0c5c"
	hfRoot   = "https://huggingface.co/MongoDB/mdbr-leaf-ir/resolve/" + modelRev + "/"
	ortVer   = "1.23.2"
)

type pinnedFile struct {
	name string
	url  string
	sum  string
}

var modelFiles = []pinnedFile{
	{name: "vocab.txt", url: hfRoot + "vocab.txt", sum: "07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3"},
	{name: "dense.safetensors", url: hfRoot + "2_Dense/model.safetensors", sum: "b3e7c0e1ef65e39a5ef1ca3bc5e4aef5feafb6e204bd161503145ed062f12c69"},
	{name: "model_quantized.onnx", url: hfRoot + "onnx/model_quantized.onnx", sum: "8c08cb4ecc00bad721b117a85738c537ceb9b85b3eedc52d9c6906fddaa55718"},
	{name: "model_quantized.onnx_data", url: hfRoot + "onnx/model_quantized.onnx_data", sum: "e77ea96a124230e23e5bac8e26c3ffe5410154973d8635673fa0220b06c13f8b"},
}

func ensureDir(ctx context.Context, dir string) error {
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
	if ok, err := fileSumOK(path, sum); err != nil {
		return err
	} else if ok {
		return nil
	}
	tmp := path + ".tmp"
	if err := downloadFile(ctx, url, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	ok, err := fileSumOK(tmp, sum)
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

func downloadFile(ctx context.Context, rawURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "zapstore-relay")
	res, err := http.DefaultClient.Do(req)
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

type ortArchive struct {
	name string
	url  string
	sum  string
	lib  string
}

func currentORT() (ortArchive, error) {
	const base = "https://github.com/microsoft/onnxruntime/releases/download/v" + ortVer + "/"
	var name, sum, libName string
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		name = "onnxruntime-osx-arm64-" + ortVer
		sum = "b4d513ab2b26f088c66891dbbc1408166708773d7cc4163de7bdca0e9bbb7856"
		libName = "libonnxruntime.dylib"
	case "darwin/amd64":
		name = "onnxruntime-osx-x86_64-" + ortVer
		sum = "d10359e16347b57d9959f7e80a225a5b4a66ed7d7e007274a15cae86836485a6"
		libName = "libonnxruntime.dylib"
	case "linux/amd64":
		name = "onnxruntime-linux-x64-" + ortVer
		sum = "1fa4dcaef22f6f7d5cd81b28c2800414350c10116f5fdd46a2160082551c5f9b"
		libName = "libonnxruntime.so." + ortVer
	case "linux/arm64":
		name = "onnxruntime-linux-aarch64-" + ortVer
		sum = "7c63c73560ed76b1fac6cff8204ffe34fe180e70d6582b5332ec094810241e5c"
		libName = "libonnxruntime.so." + ortVer
	default:
		return ortArchive{}, fmt.Errorf("onnxruntime: no build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return ortArchive{
		name: name + ".tgz",
		url:  base + name + ".tgz",
		sum:  sum,
		lib:  filepath.Join(name, "lib", libName),
	}, nil
}

func ensureORT(ctx context.Context, tools string) (string, error) {
	if p := os.Getenv("ONNXRUNTIME_LIB"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("onnxruntime: missing %s", p)
		}
		return p, nil
	}
	spec, err := currentORT()
	if err != nil {
		return "", err
	}
	lib := filepath.Join(tools, spec.lib)
	if _, err := os.Stat(lib); err == nil {
		return lib, nil
	}
	if err := os.MkdirAll(tools, 0o755); err != nil {
		return "", err
	}
	archive := filepath.Join(tools, spec.name)
	if err := ensureFile(ctx, archive, spec.url, spec.sum); err != nil {
		return "", fmt.Errorf("onnxruntime: %w", err)
	}
	if err := extractTGZ(archive, tools); err != nil {
		return "", fmt.Errorf("onnxruntime: %w", err)
	}
	if _, err := os.Stat(lib); err != nil {
		return "", fmt.Errorf("onnxruntime: archive did not contain %s", spec.lib)
	}
	return lib, nil
}

func extractTGZ(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(hdr.Name) {
			return fmt.Errorf("invalid archive path %s", hdr.Name)
		}
		target := filepath.Join(dest, name)
		rel, err := filepath.Rel(dest, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("invalid archive path %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			link := filepath.Clean(hdr.Linkname)
			if link == "." || strings.HasPrefix(link, "..") || filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("invalid archive link %s", hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		}
	}
}
