package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

// Thumb is a smaller copy of an image for lists.
type Thumb struct {
	Ext           string // ".jpg" or ".png"
	Data          []byte
	Width, Height int
}

// MakeThumb decodes the whole image (which proves it is a real picture) and returns a thumbnail,
// or nil when the image is already small enough to be its own thumbnail.
func MakeThumb(data []byte, info Info) (*Thumb, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrCorrupt
	}
	b := img.Bounds()
	if b.Dx() <= ThumbMax && b.Dy() <= ThumbMax {
		return nil, nil
	}
	w, h := fit(b.Dx(), b.Dy(), ThumbMax)
	small := resize(img, w, h)
	ext := ".png"
	if info.Mime == "image/jpeg" {
		ext = ".jpg"
	}
	out, err := encode(small, ext, 80)
	if err != nil {
		return nil, err
	}
	return &Thumb{Ext: ext, Data: out, Width: w, Height: h}, nil
}

// fit scales w×h down so the longer side is at most max.
func fit(w, h, max int) (int, int) {
	if w >= h {
		return max, maxInt(h*max/w, 1)
	}
	return maxInt(w*max/h, 1), max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	dst := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	return dst
}

func resize(img image.Image, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return dst
}

func encode(img image.Image, ext string, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if ext == ".jpg" {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	} else if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// transform turns an image clockwise by rot (0, 90, 180 or 270 degrees), then mirrors it.
func transform(src image.Image, rot int, flipH, flipV bool) *image.NRGBA {
	s := toNRGBA(src)
	sw, sh := s.Rect.Dx(), s.Rect.Dy()
	if rot == 0 && !flipH && !flipV {
		return s
	}
	dw, dh := sw, sh
	if rot == 90 || rot == 270 {
		dw, dh = sh, sw
	}
	d := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			var nx, ny int
			switch rot {
			case 90:
				nx, ny = sh-1-y, x
			case 180:
				nx, ny = sw-1-x, sh-1-y
			case 270:
				nx, ny = y, sw-1-x
			default:
				nx, ny = x, y
			}
			if flipH {
				nx = dw - 1 - nx
			}
			if flipV {
				ny = dh - 1 - ny
			}
			si := y*s.Stride + x*4
			di := ny*d.Stride + nx*4
			copy(d.Pix[di:di+4], s.Pix[si:si+4])
		}
	}
	return d
}

// orient applies an EXIF orientation value (1 to 8) so the picture is upright.
func orient(img image.Image, o int) image.Image {
	switch o {
	case 2:
		return transform(img, 0, true, false)
	case 3:
		return transform(img, 180, false, false)
	case 4:
		return transform(img, 0, false, true)
	case 5:
		return transform(img, 90, true, false)
	case 6:
		return transform(img, 90, false, false)
	case 7:
		return transform(img, 90, false, true)
	case 8:
		return transform(img, 270, false, false)
	}
	return img
}

// clean removes an upload's hidden details. A JPEG that EXIF says to turn is turned for real and saved again;
// any other JPEG keeps its exact picture data (and its colour profile) and only loses the metadata segments.
// It decodes everything, so a damaged picture is found here and not later.
func clean(data []byte, info Info) ([]byte, Info, error) {
	out := data
	switch info.Mime {
	case "image/jpeg":
		if o := exifOrientation(data); o > 1 {
			img, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				return nil, info, ErrCorrupt
			}
			img = orient(img, o)
			enc, err := encode(img, ".jpg", 92)
			if err != nil {
				return nil, info, err
			}
			out = enc
			info.Width, info.Height = img.Bounds().Dx(), img.Bounds().Dy()
		} else if s, err := stripJPEG(data); err == nil {
			out = s
		} else {
			return nil, info, err
		}
	case "image/png":
		s, err := stripPNG(data)
		if err != nil {
			return nil, info, err
		}
		out = s
	}
	if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
		return nil, info, ErrCorrupt
	}
	return out, info, nil
}

// stripJPEG drops the segments that carry hidden details: EXIF and XMP (APP1), IPTC (APP13), other application
// segments and comments. The colour profile (APP2), JFIF (APP0) and Adobe (APP14) segments stay.
func stripJPEG(b []byte) ([]byte, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, ErrCorrupt
	}
	out := make([]byte, 0, len(b))
	out = append(out, 0xFF, 0xD8)
	i := 2
	for i < len(b) {
		if b[i] != 0xFF {
			return nil, ErrCorrupt
		}
		for i < len(b) && b[i] == 0xFF {
			i++
		}
		if i >= len(b) {
			return nil, ErrCorrupt
		}
		m := b[i]
		i++
		switch {
		case m == 0xD9:
			return append(out, 0xFF, 0xD9), nil
		case m == 0x00 || m == 0x01 || (m >= 0xD0 && m <= 0xD7):
			out = append(out, 0xFF, m)
			continue
		}
		if i+2 > len(b) {
			return nil, ErrCorrupt
		}
		n := int(b[i])<<8 | int(b[i+1])
		if n < 2 || i+n > len(b) {
			return nil, ErrCorrupt
		}
		if m == 0xDA { // the picture data: keep the rest as it is
			out = append(out, 0xFF, m)
			return append(out, b[i:]...), nil
		}
		drop := m == 0xE1 || (m >= 0xE3 && m <= 0xED) || m == 0xEF || m == 0xFE
		if !drop {
			out = append(out, 0xFF, m)
			out = append(out, b[i:i+n]...)
		}
		i += n
	}
	return out, nil
}

// stripPNG drops text, time and EXIF chunks.
func stripPNG(b []byte) ([]byte, error) {
	const sig = "\x89PNG\r\n\x1a\n"
	if len(b) < 8 || string(b[:8]) != sig {
		return nil, ErrCorrupt
	}
	out := make([]byte, 0, len(b))
	out = append(out, sig...)
	i := 8
	for i < len(b) {
		if i+12 > len(b) {
			return nil, ErrCorrupt
		}
		n := int(binary.BigEndian.Uint32(b[i:]))
		if n < 0 || i+12+n > len(b) {
			return nil, ErrCorrupt
		}
		switch string(b[i+4 : i+8]) {
		case "eXIf", "tEXt", "zTXt", "iTXt", "tIME":
		default:
			out = append(out, b[i:i+12+n]...)
		}
		i += 12 + n
	}
	return out, nil
}

// exifOrientation reads the EXIF orientation of a JPEG (1 when there is none).
func exifOrientation(b []byte) int {
	i := 2
	for i+4 <= len(b) && b[i] == 0xFF {
		m := b[i+1]
		if m == 0xDA || m == 0xD9 {
			break
		}
		n := int(b[i+2])<<8 | int(b[i+3])
		if n < 2 || i+2+n > len(b) {
			break
		}
		if m == 0xE1 && n >= 2+6+8 && string(b[i+4:i+10]) == "Exif\x00\x00" {
			return tiffOrientation(b[i+10 : i+2+n])
		}
		i += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	count := int(bo.Uint16(t[off:]))
	for k := 0; k < count; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			break
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// Edit describes what the image editor does, in this order: turn, mirror, crop, shrink.
type Edit struct {
	Rotate       int // clockwise: 0, 90, 180 or 270
	FlipH, FlipV bool
	Crop         *image.Rectangle // in the turned and mirrored picture
	MaxWidth     int              // 0 keeps the size
}

func (e Edit) apply(src image.Image) (image.Image, error) {
	if e.Rotate < 0 || e.Rotate > 270 || e.Rotate%90 != 0 || e.MaxWidth < 0 || e.MaxWidth > MaxDimension {
		return nil, ErrBadEdit
	}
	img := image.Image(transform(src, e.Rotate, e.FlipH, e.FlipV))
	if c := e.Crop; c != nil {
		b := img.Bounds()
		if c.Dx() < MinCrop || c.Dy() < MinCrop || c.Min.X < 0 || c.Min.Y < 0 || c.Max.X > b.Dx() || c.Max.Y > b.Dy() {
			return nil, ErrBadEdit
		}
		cut := image.NewNRGBA(image.Rect(0, 0, c.Dx(), c.Dy()))
		draw.Draw(cut, cut.Bounds(), img, c.Min, draw.Src)
		img = cut
	}
	if w := img.Bounds().Dx(); e.MaxWidth > 0 && w > e.MaxWidth {
		h := maxInt(img.Bounds().Dy()*e.MaxWidth/w, 1)
		img = resize(img, e.MaxWidth, h)
	}
	return img, nil
}
