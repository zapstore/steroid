package catalog

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/nbd-wtf/go-nostr"
)

const (
	Version      = 1
	MediaType    = "application/vnd.zapstore.delta+tar+zstd"
	manifestKind = 30078
)

type member struct {
	Name string
	Data []byte
}

func WriteSnap(path string, events []nostr.Event) error {
	raw, err := marshalEvents(events)
	if err != nil {
		return err
	}
	body, err := pack(context.Background(), []member{{Name: "events.jsonl", Data: raw}}, 0, 0, 0, nil)
	if err != nil {
		return err
	}
	return writeAtomic(path, body)
}

func ReadSnap(path, stackPubkey string) (State, error) {
	events, err := ReadSnapEvents(path)
	if err != nil {
		return State{}, err
	}
	return Resolve(events, stackPubkey), nil
}

func ReadSnapEvents(path string) ([]nostr.Event, error) {
	members, err := unpack(path)
	if err != nil {
		return nil, err
	}
	raw, ok := members["events.jsonl"]
	if !ok {
		return nil, fmt.Errorf("%s: missing events.jsonl", path)
	}
	return unmarshalEvents(raw)
}

func marshalEvents(events []nostr.Event) ([]byte, error) {
	slices.SortFunc(events, func(a, b nostr.Event) int {
		return strings.Compare(a.ID, b.ID)
	})
	var buf bytes.Buffer
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func unmarshalEvents(raw []byte) ([]nostr.Event, error) {
	var out []nostr.Event
	for line := range bytes.SplitSeq(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event nostr.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, nil
}

func pack(ctx context.Context, members []member, from, to, sealedAt int64, signer *Signer) ([]byte, error) {
	slices.SortFunc(members, func(a, b member) int { return strings.Compare(a.Name, b.Name) })
	all := members
	if signer != nil {
		manifest, err := signManifest(ctx, from, to, sealedAt, members, *signer)
		if err != nil {
			return nil, err
		}
		all = append([]member{{Name: "manifest.json", Data: manifest}}, members...)
	}
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	zero := time.Unix(0, 0).UTC()
	for _, m := range all {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     m.Name,
			Size:     int64(len(m.Data)),
			Mode:     0o644,
			ModTime:  zero,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(m.Data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(7)))
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func unpack(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out[hdr.Name] = buf
	}
}

func signManifest(ctx context.Context, from, to, sealedAt int64, members []member, signer Signer) ([]byte, error) {
	tags := nostr.Tags{
		{"d", fmt.Sprintf("%d-%d-%d", from, to, Version)},
		{"from", fmt.Sprintf("%d", from)},
		{"to", fmt.Sprintf("%d", to)},
		{"v", fmt.Sprintf("%d", Version)},
	}
	for _, m := range members {
		sum := sha256.Sum256(m.Data)
		tags = append(tags, nostr.Tag{"file", m.Name, hex.EncodeToString(sum[:])})
	}
	event := nostr.Event{CreatedAt: nostr.Timestamp(sealedAt), Kind: manifestKind, Tags: tags}
	if err := signer.Sign(ctx, &event); err != nil {
		return nil, err
	}
	return json.Marshal(event)
}

func writeAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func filepathDir(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "."
	}
	return path[:i]
}
