package photos

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"time"
)

// Just enough EXIF and MP4 parsing to date a photo or video and stand it the
// right way up, without CGO or extra dependencies.

type exifInfo struct {
	taken       time.Time // zero when the picture has no date
	orientation int       // 1..8, 1 = upright
}

// readJPEGExif reads the EXIF block of a JPEG (it is near the start).
func readJPEGExif(r io.Reader) (exifInfo, error) {
	info := exifInfo{orientation: 1}
	br := &byteReader{r: r}
	if a, b := br.byte(), br.byte(); a != 0xFF || b != 0xD8 {
		return info, errors.New("not a jpeg")
	}
	for i := 0; i < 64 && br.err == nil; i++ {
		if br.byte() != 0xFF {
			return info, errors.New("bad jpeg marker")
		}
		marker := br.byte()
		for marker == 0xFF { // fill bytes
			marker = br.byte()
		}
		if marker == 0xD9 || marker == 0xDA { // end of image / start of scan: no EXIF
			return info, nil
		}
		n := int(br.uint16be()) - 2
		if n < 0 || br.err != nil {
			return info, errors.New("bad jpeg segment")
		}
		seg := br.bytes(n)
		if br.err != nil {
			return info, br.err
		}
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			parseTIFF(seg[6:], &info)
			return info, nil
		}
	}
	return info, br.err
}

// parseTIFF walks IFD0 (orientation) and the EXIF IFD (date taken).
func parseTIFF(b []byte, info *exifInfo) {
	if len(b) < 8 {
		return
	}
	var bo binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return
	}
	var original, digitized, offset string
	ifd := func(off uint32, visit func(tag, typ uint16, count, value uint32, raw []byte)) {
		if int(off)+2 > len(b) {
			return
		}
		n := int(bo.Uint16(b[off:]))
		for i := 0; i < n && n < 1000; i++ {
			e := int(off) + 2 + i*12
			if e+12 > len(b) {
				return
			}
			visit(bo.Uint16(b[e:]), bo.Uint16(b[e+2:]), bo.Uint32(b[e+4:]), bo.Uint32(b[e+8:]), b[e+8:e+12])
		}
	}
	ascii := func(count, value uint32, raw []byte) string {
		if count <= 4 {
			return strings.TrimRight(string(raw[:count]), "\x00 ")
		}
		if int(value)+int(count) > len(b) {
			return ""
		}
		return strings.TrimRight(string(b[value:value+count]), "\x00 ")
	}
	var exifIFD uint32
	ifd(bo.Uint32(b[4:]), func(tag, typ uint16, count, value uint32, raw []byte) {
		switch tag {
		case 0x0112: // Orientation (SHORT)
			if o := int(bo.Uint16(raw)); o >= 1 && o <= 8 {
				info.orientation = o
			}
		case 0x8769: // Exif IFD pointer
			exifIFD = value
		}
	})
	if exifIFD != 0 {
		ifd(exifIFD, func(tag, typ uint16, count, value uint32, raw []byte) {
			switch tag {
			case 0x9003:
				original = ascii(count, value, raw)
			case 0x9004:
				digitized = ascii(count, value, raw)
			case 0x9011: // OffsetTimeOriginal, e.g. "+04:00"
				offset = ascii(count, value, raw)
			}
		})
	}
	date := original
	if date == "" {
		date = digitized
	}
	if t, ok := parseExifTime(date, offset); ok {
		info.taken = t
	}
}

// parseExifTime reads "2026:10:09 14:03:22", in the given offset or local time.
func parseExifTime(s, offset string) (time.Time, bool) {
	if len(s) < 19 || strings.HasPrefix(s, "0000") {
		return time.Time{}, false
	}
	if offset != "" {
		if t, err := time.Parse("2006:01:02 15:04:05-07:00", s[:19]+offset); err == nil {
			return t, true
		}
	}
	t, err := time.ParseInLocation("2006:01:02 15:04:05", s[:19], time.Local)
	return t, err == nil
}

// readMP4Created reads the recording time of an MP4/MOV from its movie header
// ("moov" > "mvhd"); seconds since 1904, in UTC.
func readMP4Created(r io.ReadSeeker) (time.Time, error) {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return time.Time{}, err
	}
	find := func(from, to int64, want string) (int64, int64, error) {
		for pos := from; pos+8 <= to; {
			var h [16]byte
			if _, err := r.Seek(pos, io.SeekStart); err != nil {
				return 0, 0, err
			}
			if _, err := io.ReadFull(r, h[:8]); err != nil {
				return 0, 0, err
			}
			size := int64(binary.BigEndian.Uint32(h[:4]))
			head := int64(8)
			if size == 1 { // 64-bit size
				if _, err := io.ReadFull(r, h[8:16]); err != nil {
					return 0, 0, err
				}
				size = int64(binary.BigEndian.Uint64(h[8:16]))
				head = 16
			} else if size == 0 {
				size = to - pos
			}
			if size < head {
				return 0, 0, errors.New("bad atom")
			}
			if string(h[4:8]) == want {
				return pos + head, pos + size, nil
			}
			pos += size
		}
		return 0, 0, errors.New("atom not found: " + want)
	}
	start, stop, err := find(0, end, "moov")
	if err != nil {
		return time.Time{}, err
	}
	body, _, err := find(start, stop, "mvhd")
	if err != nil {
		return time.Time{}, err
	}
	var v [12]byte
	if _, err := r.Seek(body, io.SeekStart); err != nil {
		return time.Time{}, err
	}
	if _, err := io.ReadFull(r, v[:]); err != nil {
		return time.Time{}, err
	}
	var secs uint64
	if v[0] == 1 {
		secs = binary.BigEndian.Uint64(v[4:12])
	} else {
		secs = uint64(binary.BigEndian.Uint32(v[4:8]))
	}
	if secs == 0 {
		return time.Time{}, errors.New("no creation time")
	}
	t := time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(secs) * time.Second)
	if t.Year() < 1971 || t.After(time.Now().Add(48*time.Hour)) {
		return time.Time{}, errors.New("implausible creation time")
	}
	return t, nil
}

// byteReader reads big-endian values and remembers the first error.
type byteReader struct {
	r   io.Reader
	err error
	buf [2]byte
}

func (b *byteReader) byte() byte {
	if b.err != nil {
		return 0
	}
	_, b.err = io.ReadFull(b.r, b.buf[:1])
	return b.buf[0]
}

func (b *byteReader) uint16be() uint16 {
	if b.err != nil {
		return 0
	}
	_, b.err = io.ReadFull(b.r, b.buf[:2])
	return binary.BigEndian.Uint16(b.buf[:2])
}

func (b *byteReader) bytes(n int) []byte {
	if b.err != nil || n > 1<<20 {
		if b.err == nil {
			b.err = errors.New("segment too large")
		}
		return nil
	}
	out := make([]byte, n)
	_, b.err = io.ReadFull(b.r, out)
	return out
}
