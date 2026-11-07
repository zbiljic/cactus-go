package cactus

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"reflect"
	"testing"
)

// testTIFF is an Exif TIFF header whose first IFD holds an Orientation tag
// after one unrelated tag.
func testTIFF(order binary.ByteOrder, kind uint16, orientation uint32) []byte {
	tiff := make([]byte, 8+2+2*12+4)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 2)
	order.PutUint16(tiff[10:], 0x010f) // Make
	order.PutUint16(tiff[22:], 0x0112)
	order.PutUint16(tiff[24:], kind)
	order.PutUint32(tiff[26:], 1)
	if kind == 3 {
		order.PutUint16(tiff[30:], uint16(orientation))
	} else {
		order.PutUint32(tiff[30:], orientation)
	}
	return tiff
}

// withJPEGExif inserts an APP1 Exif segment after a JPEG's start marker,
// behind an unrelated APP0 segment.
func withJPEGExif(t *testing.T, img image.Image, tiff []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	segment := append([]byte("Exif\x00\x00"), tiff...)
	out := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 4, 'J', 'F', 0xff, 0xe1}
	out = binary.BigEndian.AppendUint16(out, uint16(len(segment)+2))
	out = append(out, segment...)
	return append(out, encoded.Bytes()[2:]...)
}

// withPNGExif inserts an eXIf chunk after a PNG's IHDR chunk.
func withPNGExif(t *testing.T, img image.Image, exif []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	const ihdrEnd = 8 + 12 + 13
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(exif)))
	chunk = append(chunk, "eXIf"...)
	chunk = append(chunk, exif...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	return append(append(append([]byte(nil), data[:ihdrEnd]...), chunk...), data[ihdrEnd:]...)
}

// testOrientationImage is 3x2 with pixel values 0 to 5 in reading order.
func testOrientationImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for i := range 6 {
		img.SetNRGBA(i%3, i/3, color.NRGBA{uint8(i), uint8(i), uint8(i), 0xff})
	}
	return img
}

// testOrientations lists, per orientation, the upright pixels in reading
// order and the upright width.
var testOrientations = map[int]struct {
	pixels []byte
	width  int
}{
	1: {[]byte{0, 1, 2, 3, 4, 5}, 3},
	2: {[]byte{2, 1, 0, 5, 4, 3}, 3}, // Mirror horizontally.
	3: {[]byte{5, 4, 3, 2, 1, 0}, 3}, // Rotate 180°.
	4: {[]byte{3, 4, 5, 0, 1, 2}, 3}, // Mirror vertically.
	5: {[]byte{0, 3, 1, 4, 2, 5}, 2}, // Transpose.
	6: {[]byte{3, 0, 4, 1, 5, 2}, 2}, // Rotate 90° clockwise.
	7: {[]byte{5, 2, 4, 1, 3, 0}, 2}, // Transverse.
	8: {[]byte{2, 5, 1, 4, 0, 3}, 2}, // Rotate 90° counterclockwise.
}

func TestOrient(t *testing.T) {
	t.Parallel()
	source := &RGBImage{Width: 3, Height: 2}
	for i := range 6 {
		source.Pix = append(source.Pix, byte(i), byte(i), byte(i))
	}
	for orientation, want := range testOrientations {
		got := orient(source, orientation)
		gray := make([]byte, 0, 6)
		for i := 0; i < len(got.Pix); i += 3 {
			gray = append(gray, got.Pix[i])
		}
		if !reflect.DeepEqual(gray, want.pixels) || got.Width != want.width || got.Height != 6/want.width {
			t.Fatalf("orientation %d = %v %dx%d, want %v", orientation, gray, got.Width, got.Height, want.pixels)
		}
	}
	for _, unknown := range []int{0, 9, -1} {
		if orient(source, unknown) != source {
			t.Fatalf("orientation %d changed the image", unknown)
		}
	}
}

func TestDecodeImageAppliesExifOrientation(t *testing.T) {
	t.Parallel()
	img := testOrientationImage()
	for orientation, want := range testOrientations {
		for name, data := range map[string][]byte{
			"PNG little-endian SHORT": withPNGExif(t, img, testTIFF(binary.LittleEndian, 3, uint32(orientation))),
			"PNG Exif prefix":         withPNGExif(t, img, append([]byte("Exif\x00\x00"), testTIFF(binary.BigEndian, 3, uint32(orientation))...)),
			"JPEG big-endian SHORT":   withJPEGExif(t, img, testTIFF(binary.BigEndian, 3, uint32(orientation))),
			"JPEG little-endian LONG": withJPEGExif(t, img, testTIFF(binary.LittleEndian, 4, uint32(orientation))),
		} {
			if got := exifOrientation(data); got != orientation {
				t.Fatalf("%s orientation = %d, want %d", name, got, orientation)
			}
			decoded, err := DecodeImage(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if orientation == 1 {
				if _, ok := decoded.(*RGBImage); ok {
					t.Fatalf("%s: upright image was converted", name)
				}
				continue
			}
			if size := decoded.Bounds().Size(); size.X != want.width || size.Y != 6/want.width {
				t.Fatalf("%s orientation %d size = %v", name, orientation, size)
			}
			if name[:3] == "PNG" {
				rgb := decoded.(*RGBImage)
				for i, value := range want.pixels {
					if rgb.Pix[i*3] != value {
						t.Fatalf("%s orientation %d pixels = %v, want %v", name, orientation, rgb.Pix, want.pixels)
					}
				}
			}
		}
	}
}

func TestExifOrientationIgnoresInvalidData(t *testing.T) {
	t.Parallel()
	img := testOrientationImage()
	valid := withJPEGExif(t, img, testTIFF(binary.LittleEndian, 3, 6))
	var plainJPEG, plainPNG bytes.Buffer
	if err := jpeg.Encode(&plainJPEG, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&plainPNG, img); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"empty":        nil,
		"GIF":          []byte("GIF89a"),
		"plain JPEG":   plainJPEG.Bytes(),
		"plain PNG":    plainPNG.Bytes(),
		"value 9":      withPNGExif(t, img, testTIFF(binary.LittleEndian, 3, 9)),
		"value 0":      withJPEGExif(t, img, testTIFF(binary.BigEndian, 3, 0)),
		"ASCII type":   withJPEGExif(t, img, testTIFF(binary.BigEndian, 2, 6)),
		"bad TIFF":     withJPEGExif(t, img, []byte("XX*\x00\x08\x00\x00\x00")),
		"bad IFD":      withPNGExif(t, img, []byte("II*\x00\xff\xff\x00\x00")),
		"junk bytes":   append([]byte{0xff, 0xd8, 0, 0x42}, valid[2:]...),
		"XMP segment":  append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, 8, 'h', 't', 't', 'p', ':', '/'}, valid[2:]...),
		"short header": {0xff, 0xd8, 0xff},
	} {
		want := 1
		if name == "XMP segment" || name == "junk bytes" {
			want = 6 // The Exif segment after XMP or junk bytes is still found.
		}
		if got := exifOrientation(data); got != want {
			t.Fatalf("%s orientation = %d, want %d", name, got, want)
		}
	}
	for _, data := range [][]byte{valid, withPNGExif(t, img, testTIFF(binary.BigEndian, 4, 8))} {
		for size := range len(data) {
			if got := exifOrientation(data[:size]); got < 1 || got > 8 {
				t.Fatalf("truncated to %d bytes: %d", size, got)
			}
		}
	}
}

func FuzzExifOrientation(f *testing.F) {
	f.Add([]byte{0xff, 0xd8, 0xff, 0xe1, 0, 8, 'E', 'x', 'i', 'f', 0, 0})
	f.Add([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x08eXIfII*\x00\x08\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if got := exifOrientation(data); got < 1 || got > 8 {
			t.Fatalf("orientation %d", got)
		}
	})
}
