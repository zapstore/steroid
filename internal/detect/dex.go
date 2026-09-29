package detect

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// classPaths returns slash paths for classes defined in one DEX file.
// A descriptor Lcom/example/App; becomes /com/example/App.
func dexStrings(dex []byte) []string {
	if len(dex) < 112 || string(dex[:4]) != "dex\n" {
		return nil
	}
	stringN := int(u32(dex, 56))
	stringOff := int(u32(dex, 60))
	if stringN < 0 || stringOff < 0 {
		return nil
	}
	out := make([]string, 0, 64)
	for i := 0; i < stringN; i++ {
		off := stringOff + i*4
		if off < 0 || off+4 > len(dex) {
			break
		}
		s, err := mutf8(dex, int(u32(dex, off)))
		if err != nil || s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func classPaths(dex []byte) ([]string, error) {
	if len(dex) < 112 || string(dex[:4]) != "dex\n" {
		return nil, fmt.Errorf("not a dex file")
	}
	stringN := int(u32(dex, 56))
	stringOff := int(u32(dex, 60))
	typeN := int(u32(dex, 64))
	typeOff := int(u32(dex, 68))
	classN := int(u32(dex, 96))
	classOff := int(u32(dex, 100))
	if stringN < 0 || typeN < 0 || classN < 0 {
		return nil, fmt.Errorf("dex counts")
	}
	out := make([]string, 0, classN)
	for i := 0; i < classN; i++ {
		off := classOff + i*32
		if off < 0 || off+4 > len(dex) {
			return nil, fmt.Errorf("class_def %d", i)
		}
		typeIdx := int(u32(dex, off))
		if typeIdx < 0 || typeIdx >= typeN {
			return nil, fmt.Errorf("class_def %d type %d", i, typeIdx)
		}
		descIdx := int(u32(dex, typeOff+typeIdx*4))
		if descIdx < 0 || descIdx >= stringN {
			return nil, fmt.Errorf("type %d string %d", typeIdx, descIdx)
		}
		dataOff := int(u32(dex, stringOff+descIdx*4))
		s, err := mutf8(dex, dataOff)
		if err != nil {
			return nil, err
		}
		if path, ok := descriptorPath(s); ok {
			out = append(out, path)
		}
	}
	return out, nil
}

func u32(b []byte, off int) uint32 {
	if off < 0 || off+4 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint32(b[off:])
}

func mutf8(b []byte, off int) (string, error) {
	if off < 0 || off >= len(b) {
		return "", fmt.Errorf("string offset %d", off)
	}
	_, n := uleb(b[off:])
	start := off + n
	if start > len(b) {
		return "", fmt.Errorf("string offset %d", off)
	}
	end := start
	for end < len(b) && b[end] != 0 {
		end++
	}
	return string(b[start:end]), nil
}

func uleb(b []byte) (uint32, int) {
	var v uint32
	for i := 0; i < len(b) && i < 5; i++ {
		v |= uint32(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 1
}

func descriptorPath(desc string) (string, bool) {
	if !strings.HasPrefix(desc, "L") || !strings.HasSuffix(desc, ";") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(desc, "L"), ";")
	if inner == "" || strings.Contains(inner, ";") {
		return "", false
	}
	return "/" + inner, true
}
