package photos

import (
	"encoding/binary"
	"errors"
	"io"
	"path"
	"strings"

	"alfaos/alfad/internal/files"
)

// Turning a photo without losing anything: a JPEG says which way up it goes
// in its EXIF Orientation tag, so turning it only rewrites those two bytes
// in place. The pixels are never decoded or saved again.

// ErrNoOrientation means the picture has no orientation tag to change (not a
// JPEG, or one without EXIF); it can only be turned by saving it again.
var ErrNoOrientation = errors.New("this picture can only be turned by saving it again")

// headBytes is how much of a JPEG is read to find its EXIF block (it comes first).
const headBytes = 256 << 10

// orientationAt finds the Orientation tag in the start of a JPEG: the offset
// of its 2-byte value in the file, the value (1..8) and the byte order.
func orientationAt(head []byte) (off int, val int, bo binary.ByteOrder, ok bool) {
	if len(head) < 4 || head[0] != 0xFF || head[1] != 0xD8 {
		return 0, 0, nil, false
	}
	p := 2
	for seg := 0; seg < 64 && p+4 <= len(head); seg++ {
		if head[p] != 0xFF {
			return 0, 0, nil, false
		}
		marker := head[p+1]
		for marker == 0xFF && p+2 < len(head) { // fill bytes
			p++
			marker = head[p+1]
		}
		if marker == 0xD9 || marker == 0xDA {
			return 0, 0, nil, false
		}
		n := int(binary.BigEndian.Uint16(head[p+2:]))
		start, end := p+4, p+2+n
		if n < 2 || end > len(head) {
			return 0, 0, nil, false
		}
		if marker == 0xE1 && n >= 8 && string(head[start:start+6]) == "Exif\x00\x00" {
			tiff := head[start+6 : end]
			o, v, b, found := tiffOrientation(tiff)
			if !found {
				return 0, 0, nil, false
			}
			return start + 6 + o, v, b, true
		}
		p = end
	}
	return 0, 0, nil, false
}

// tiffOrientation finds the Orientation entry of IFD0 in a TIFF block.
func tiffOrientation(b []byte) (off int, val int, bo binary.ByteOrder, ok bool) {
	if len(b) < 8 {
		return 0, 0, nil, false
	}
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0, 0, nil, false
	}
	ifd := int(bo.Uint32(b[4:]))
	if ifd+2 > len(b) {
		return 0, 0, nil, false
	}
	n := int(bo.Uint16(b[ifd:]))
	for i := 0; i < n && i < 1000; i++ {
		e := ifd + 2 + i*12
		if e+12 > len(b) {
			return 0, 0, nil, false
		}
		if bo.Uint16(b[e:]) != 0x0112 {
			continue
		}
		if bo.Uint16(b[e+2:]) != 3 || bo.Uint32(b[e+4:]) != 1 { // one SHORT
			return 0, 0, nil, false
		}
		v := int(bo.Uint16(b[e+8:]))
		if v < 1 || v > 8 {
			v = 1
		}
		return e + 8, v, bo, true
	}
	return 0, 0, nil, false
}

// turnOrientation is the orientation after quarter turns clockwise
// (negative: anticlockwise). Mirrored pictures stay mirrored.
func turnOrientation(o, turns int) int {
	plain := []int{1, 6, 3, 8}
	mirrored := []int{2, 7, 4, 5}
	q := ((turns % 4) + 4) % 4
	for _, cycle := range [][]int{plain, mirrored} {
		for i, v := range cycle {
			if v == o {
				return cycle[(i+q)%4]
			}
		}
	}
	return plain[q]
}

// Rotate turns a photo by quarter turns clockwise without re-encoding it,
// and returns its new orientation. ErrNoOrientation when it can't.
func (l *Library) Rotate(root, rel string, turns int) (int, error) {
	rel, err := files.Clean(rel)
	if err != nil {
		return 0, err
	}
	ext := strings.ToLower(path.Ext(rel))
	if !imageExt[ext] {
		return 0, files.ErrBadPath
	}
	if ext != ".jpg" && ext != ".jpeg" {
		return 0, ErrNoOrientation
	}
	f, _, err := l.files.Open(root, rel)
	if err != nil {
		return 0, err
	}
	head := make([]byte, headBytes)
	n, err := io.ReadFull(f, head)
	f.Close()
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return 0, err
	}
	off, val, bo, ok := orientationAt(head[:n])
	if !ok {
		return 0, ErrNoOrientation
	}
	next := turnOrientation(val, turns)
	b := make([]byte, 2)
	bo.PutUint16(b, uint16(next))
	if err := l.files.PatchAt(root, rel, int64(off), b); err != nil {
		return 0, err
	}
	return next, nil
}
