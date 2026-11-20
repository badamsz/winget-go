package mszyml

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

func TestDecodeRealSample(t *testing.T) {
	b, err := os.ReadFile("testdata/7zip.versionData.mszyml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "sV: 1.0\nvD:\n") || !strings.Contains(s, "rP: manifests/7/7zip/7zip/") {
		t.Fatalf("unexpected content: %.200q", s)
	}
}

// encode builds a multi-block MSZIP buffer, each block using the previous
// block as a dictionary, to exercise the dictionary path.
func encode(t *testing.T, data []byte, chunk int) []byte {
	var buf bytes.Buffer
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], signature)
	binary.LittleEndian.PutUint16(hdr[4:], headerSize)
	binary.LittleEndian.PutUint64(hdr[8:], uint64(len(data)))
	binary.LittleEndian.PutUint64(hdr[16:], uint64(chunk))
	buf.Write(hdr)
	var prev []byte
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		var blk bytes.Buffer
		blk.WriteString("CK")
		w, err := flate.NewWriterDict(&blk, flate.BestCompression, prev)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data[i:end])
		w.Close()
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(blk.Len()))
		buf.Write(n[:])
		buf.Write(blk.Bytes())
		prev = data[i:end]
	}
	return buf.Bytes()
}

func TestDecodeMultiBlock(t *testing.T) {
	data := []byte(strings.Repeat("- v: 1.2.3\n  rP: manifests/a/b/c\n", 5000))
	got, err := Decode(encode(t, data, 32*1024))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("round trip mismatch")
	}
}

func TestDecodeBadSignature(t *testing.T) {
	if _, err := Decode(make([]byte, 40)); err == nil {
		t.Fatal("expected error")
	}
}
