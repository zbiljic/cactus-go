package cactus

import (
	"bytes"
	"encoding/binary"
)

// exifOrientation returns the EXIF Orientation tag (1 to 8) of a JPEG (APP1
// Exif segment) or PNG (eXIf chunk), or 1 when it is absent or invalid.
func exifOrientation(data []byte) int {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8}):
		return jpegOrientation(data)
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return pngOrientation(data)
	}
	return 1
}

func jpegOrientation(data []byte) int {
	position := 2
	for position+4 <= len(data) {
		if data[position] != 0xff { // Skip junk between segments, as image/jpeg and Pillow do.
			position++
			continue
		}
		marker := data[position+1]
		switch {
		case marker == 0xff: // Fill byte.
			position++
			continue
		case marker == 0xda || marker == 0xd9: // Image data or the end.
			return 1
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7): // No length.
			position += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(data[position+2:]))
		if length < 2 || position+2+length > len(data) {
			return 1
		}
		segment := data[position+4 : position+2+length]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return tiffOrientation(segment[6:])
		}
		position += 2 + length
	}
	return 1
}

func pngOrientation(data []byte) int {
	position := 8
	for position+8 <= len(data) {
		length := uint64(binary.BigEndian.Uint32(data[position:]))
		kind := string(data[position+4 : position+8])
		if length > uint64(len(data)-position-8) {
			return 1
		}
		chunk := data[position+8 : position+8+int(length)]
		switch kind {
		case "eXIf":
			return tiffOrientation(bytes.TrimPrefix(chunk, []byte("Exif\x00\x00")))
		case "IEND":
			return 1
		}
		position += 12 + int(length)
	}
	return 1
}

// tiffOrientation reads tag 0x0112 from the first IFD of a TIFF header.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:]) != 42 {
		return 1
	}
	directory := uint64(order.Uint32(tiff[4:]))
	if directory+2 > uint64(len(tiff)) {
		return 1
	}
	entries := uint64(order.Uint16(tiff[directory:]))
	for i := range entries {
		entry := directory + 2 + i*12
		if entry+12 > uint64(len(tiff)) {
			return 1
		}
		if order.Uint16(tiff[entry:]) != 0x0112 || order.Uint32(tiff[entry+4:]) != 1 {
			continue
		}
		var value uint32
		switch order.Uint16(tiff[entry+2:]) {
		case 3: // SHORT
			value = uint32(order.Uint16(tiff[entry+8:]))
		case 4: // LONG
			value = order.Uint32(tiff[entry+8:])
		}
		if value >= 1 && value <= 8 {
			return int(value)
		}
		return 1
	}
	return 1
}

// orient rotates or flips packed RGB upright for an EXIF orientation, as
// Pillow's ImageOps.exif_transpose does. Orientation 1 and unknown values
// return the image unchanged.
func orient(img *RGBImage, orientation int) *RGBImage {
	if orientation < 2 || orientation > 8 {
		return img
	}
	width, height := img.Width, img.Height
	outputWidth, outputHeight := width, height
	if orientation >= 5 {
		outputWidth, outputHeight = height, width
	}
	output := &RGBImage{Pix: make([]byte, len(img.Pix)), Width: outputWidth, Height: outputHeight}
	i := 0
	for y := range outputHeight {
		for x := range outputWidth {
			var sourceX, sourceY int
			switch orientation {
			case 2: // Mirror horizontally.
				sourceX, sourceY = width-1-x, y
			case 3: // Rotate 180°.
				sourceX, sourceY = width-1-x, height-1-y
			case 4: // Mirror vertically.
				sourceX, sourceY = x, height-1-y
			case 5: // Transpose.
				sourceX, sourceY = y, x
			case 6: // Rotate 90° clockwise.
				sourceX, sourceY = y, height-1-x
			case 7: // Transverse.
				sourceX, sourceY = width-1-y, height-1-x
			default: // 8: rotate 90° counterclockwise.
				sourceX, sourceY = width-1-y, x
			}
			copy(output.Pix[i:i+3], img.Pix[(sourceY*width+sourceX)*3:])
			i += 3
		}
	}
	return output
}
