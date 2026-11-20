// Package mszyml decodes the ".mszyml" files served by the winget
// pre-indexed source (v2). They are YAML documents compressed with the
// Windows Compression API "buffer" format using the MSZIP algorithm:
//
//	bytes   0A 51 E5 C0 (signature)
//	uint16  header size / algorithm info (ignored)
//	uint16  header checksum (ignored)
//	uint64  total uncompressed size
//	uint64  chunk size
//	repeat:
//	  uint32 compressed block size
//	  "CK" + raw DEFLATE data
//
// Each MSZIP block is an independent DEFLATE stream that may reference
// the previous block's (up to 32 KiB) output as a preset dictionary.
package mszyml

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	signature  = 0xC0E5510A // bytes 0A 51 E5 C0
	headerSize = 24
	window     = 32 * 1024
)

// Decode decompresses an mszyml buffer.
func Decode(b []byte) ([]byte, error) {
	if len(b) < headerSize {
		return nil, errors.New("mszyml: buffer too small")
	}
	if binary.LittleEndian.Uint32(b[0:4]) != signature {
		return nil, errors.New("mszyml: bad signature")
	}
	total := binary.LittleEndian.Uint64(b[8:16])
	if total > 1<<30 {
		return nil, fmt.Errorf("mszyml: unreasonable uncompressed size %d", total)
	}
	out := make([]byte, 0, total)
	off := headerSize
	for off < len(b) && uint64(len(out)) < total {
		if off+4 > len(b) {
			return nil, errors.New("mszyml: truncated block header")
		}
		n := int(binary.LittleEndian.Uint32(b[off : off+4]))
		off += 4
		if n < 2 || off+n > len(b) {
			return nil, errors.New("mszyml: truncated block")
		}
		blk := b[off : off+n]
		off += n
		if blk[0] != 'C' || blk[1] != 'K' {
			return nil, errors.New("mszyml: missing MSZIP block signature")
		}
		var dict []byte
		if len(out) > 0 {
			start := len(out) - window
			if start < 0 {
				start = 0
			}
			dict = out[start:]
		}
		r := flate.NewReaderDict(bytes.NewReader(blk[2:]), dict)
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("mszyml: inflate: %w", err)
		}
		out = append(out, data...)
	}
	if uint64(len(out)) != total {
		return nil, fmt.Errorf("mszyml: size mismatch: got %d want %d", len(out), total)
	}
	return out, nil
}
