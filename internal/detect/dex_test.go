package detect

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestClassPaths(t *testing.T) {
	dex := testDEX(t, "Lcom/example/App;", "Lcom/google/firebase/analytics/FirebaseAnalytics;")
	got, err := classPaths(dex)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/com/example/App", "/com/google/firebase/analytics/FirebaseAnalytics"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

func testDEX(t *testing.T, descriptors ...string) []byte {
	t.Helper()
	var data bytes.Buffer
	var offs []uint32
	for _, desc := range descriptors {
		offs = append(offs, uint32(data.Len()))
		data.WriteByte(byte(len(desc)))
		data.WriteString(desc)
		data.WriteByte(0)
	}
	headerSize := 0x70
	stringOff := headerSize
	typeOff := stringOff + 4*len(descriptors)
	classOff := typeOff + 4*len(descriptors)
	dataOff := classOff + 32*len(descriptors)
	fileSize := dataOff + data.Len()

	buf := make([]byte, fileSize)
	copy(buf, "dex\n035\x00")
	binary.LittleEndian.PutUint32(buf[32:], uint32(fileSize))
	binary.LittleEndian.PutUint32(buf[36:], uint32(headerSize))
	binary.LittleEndian.PutUint32(buf[40:], 0x12345678)
	binary.LittleEndian.PutUint32(buf[56:], uint32(len(descriptors)))
	binary.LittleEndian.PutUint32(buf[60:], uint32(stringOff))
	binary.LittleEndian.PutUint32(buf[64:], uint32(len(descriptors)))
	binary.LittleEndian.PutUint32(buf[68:], uint32(typeOff))
	binary.LittleEndian.PutUint32(buf[96:], uint32(len(descriptors)))
	binary.LittleEndian.PutUint32(buf[100:], uint32(classOff))
	for i, off := range offs {
		binary.LittleEndian.PutUint32(buf[stringOff+4*i:], uint32(dataOff)+off)
		binary.LittleEndian.PutUint32(buf[typeOff+4*i:], uint32(i))
		binary.LittleEndian.PutUint32(buf[classOff+32*i:], uint32(i))
	}
	copy(buf[dataOff:], data.Bytes())
	return buf
}
