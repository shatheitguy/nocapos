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
	camera      Camera
}

// Camera is what the camera wrote about a shot (shown in the info panel).
type Camera struct {
	Make     string  `json:"make,omitempty"`
	Model    string  `json:"model,omitempty"`
	Lens     string  `json:"lens,omitempty"`
	Exposure float64 `json:"exposure,omitempty"` // seconds
	FNumber  float64 `json:"f_number,omitempty"`
	ISO      int     `json:"iso,omitempty"`
	Focal    float64 `json:"focal,omitempty"`    // mm
	Focal35  int     `json:"focal_35,omitempty"` // 35 mm equivalent
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
	rational := func(typ uint16, value uint32) float64 {
		if (typ != 5 && typ != 10) || int(value)+8 > len(b) {
			return 0
		}
		num, den := bo.Uint32(b[value:]), bo.Uint32(b[value+4:])
		if den == 0 {
			return 0
		}
		if typ == 10 {
			return float64(int32(num)) / float64(int32(den))
		}
		return float64(num) / float64(den)
	}
	short := func(typ uint16, raw []byte) int {
		switch typ {
		case 3:
			return int(bo.Uint16(raw))
		case 4:
			return int(bo.Uint32(raw))
		}
		return 0
	}
	cam := &info.camera
	var exifIFD uint32
	ifd(bo.Uint32(b[4:]), func(tag, typ uint16, count, value uint32, raw []byte) {
		switch tag {
		case 0x010F:
			cam.Make = ascii(count, value, raw)
		case 0x0110:
			cam.Model = ascii(count, value, raw)
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
			case 0x829A:
				cam.Exposure = rational(typ, value)
			case 0x829D:
				cam.FNumber = rational(typ, value)
			case 0x8827:
				cam.ISO = short(typ, raw)
			case 0x920A:
				cam.Focal = rational(typ, value)
			case 0xA405:
				cam.Focal35 = short(typ, raw)
			case 0xA434:
				cam.Lens = ascii(count, value, raw)
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

// readMP4Created reads the recording time of an MP4/MOV.
func readMP4Created(r io.ReadSeeker) (time.Time, error) {
	t, _, err := readMP4Header(r)
	if err == nil && t.IsZero() {
		err = errors.New("no creation time")
	}
	return t, err
}

// readMP4Header reads the recording time (zero when missing or implausible)
// and the length of an MP4/MOV from its movie header ("moov" > "mvhd").
func readMP4Header(r io.ReadSeeker) (created time.Time, length time.Duration, err error) {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return time.Time{}, 0, err
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
		return time.Time{}, 0, err
	}
	body, _, err := find(start, stop, "mvhd")
	if err != nil {
		return time.Time{}, 0, err
	}
	// version 0: flags, created, modified, timescale, duration (32-bit);
	// version 1: the times and duration are 64-bit.
	var v [32]byte
	if _, err := r.Seek(body, io.SeekStart); err != nil {
		return time.Time{}, 0, err
	}
	if n, err := io.ReadFull(r, v[:]); err != nil && n < 20 {
		return time.Time{}, 0, err
	}
	var secs, scale, dur uint64
	if v[0] == 1 {
		secs = binary.BigEndian.Uint64(v[4:12])
		scale = uint64(binary.BigEndian.Uint32(v[20:24]))
		dur = binary.BigEndian.Uint64(v[24:32])
	} else {
		secs = uint64(binary.BigEndian.Uint32(v[4:8]))
		scale = uint64(binary.BigEndian.Uint32(v[12:16]))
		dur = uint64(binary.BigEndian.Uint32(v[16:20]))
	}
	if scale > 0 && dur > 0 && dur != 0xFFFFFFFF && dur/scale < 7*24*3600 {
		length = time.Duration(float64(dur) / float64(scale) * float64(time.Second))
	}
	if secs != 0 {
		t := time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(secs) * time.Second)
		if t.Year() >= 1971 && !t.After(time.Now().Add(48*time.Hour)) {
			created = t
		}
	}
	return created, length, nil
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
