package cactus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // Register the formats DecodeImage reads.
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// DefaultVisionBufferSize holds descriptions with polygon or bitmap masks for
// many objects, as upstream does.
const DefaultVisionBufferSize = 16 << 20

// VisionSection names a top-level part of a Description.
type VisionSection string

const (
	SectionScene     VisionSection = "scene"
	SectionObjects   VisionSection = "objects"
	SectionRegions   VisionSection = "regions"
	SectionText      VisionSection = "text"
	SectionRelations VisionSection = "relations"
	SectionEvents    VisionSection = "events"
	SectionReasoning VisionSection = "reasoning"
)

// VisionField names a per-item detail of objects and text lines. IDs, names
// and text strings are always present.
type VisionField string

const (
	FieldBBox        VisionField = "bbox"
	FieldScore       VisionField = "score"
	FieldPoint       VisionField = "point"
	FieldMask        VisionField = "mask"
	FieldPosition    VisionField = "position"
	FieldDescription VisionField = "description"
)

// MaskFormat selects how object masks are encoded.
type MaskFormat string

const (
	// MaskLargest outlines the largest part of the mask as one polygon.
	MaskLargest MaskFormat = "largest"
	// MaskMerged outlines the whole mask as one polygon.
	MaskMerged MaskFormat = "merged"
	// MaskPolygons returns one polygon per part of the mask.
	MaskPolygons MaskFormat = "polygons"
	// MaskBitmap returns a run-length encoded pixel mask.
	MaskBitmap MaskFormat = "bitmap"
)

// DescribeOptions controls a Mirage request. Zero values select the engine
// defaults. The engine validates names and ranges and reports invalid options
// as a *NativeError.
type DescribeOptions struct {
	// Sections limits the response to these sections; nil returns all.
	Sections []VisionSection
	// Fields selects per-item details; nil returns all, and an empty
	// non-nil slice returns only IDs, names and text strings.
	Fields []VisionField
	// Confidence is the object score threshold in (0, 1] ("conf").
	Confidence float64
	// TextConfidence is the text line score threshold in (0, 1] ("text_conf").
	TextConfidence float64
	MaxObjects     int
	MaxTextLines   int
	// MaxTokens bounds the generated description.
	MaxTokens  int
	MaskFormat MaskFormat
	// NMS turns the engine's non-maximum suppression of overlapping
	// detections on or off; nil keeps the engine default.
	NMS *bool
	// Language selects the language of names and descriptions: empty for
	// English, or de, fr, es, it, nl or pl. The engine rejects languages it
	// does not support.
	Language string
	// Keywords are words and phrases to favor.
	Keywords []string
	// Extra holds other cactus_describe options, such as keyword_bias,
	// mask_threshold or mask_tolerance. It cannot repeat an option that has
	// a field above, form, language or keywords.
	Extra map[string]any
}

// MirageConfig selects vision weights and the Cactus engine that runs them.
// LibraryPath and CacheDir follow Config. Empty WeightsPath fetches Mirage.
// Zero BufferSize selects DefaultVisionBufferSize.
type MirageConfig struct {
	WeightsPath string
	LibraryPath string
	CacheDir    string
	BufferSize  int
}

// Mirage describes images and fills forms from them. Vision, Needle 3 text
// and Whistle speech models share a serialized process-global engine.
type Mirage struct {
	runtime     *processRuntime
	weightsPath string
	buffer      []byte
	closed      bool
}

// Description is a Mirage response. Sections not requested are empty.
type Description struct {
	Scene     map[string]string `json:"scene,omitempty"`
	Objects   []VisionObject    `json:"objects,omitempty"`
	Regions   []json.RawMessage `json:"regions,omitempty"`
	Text      []TextLine        `json:"text,omitempty"`
	Relations []Relation        `json:"relations,omitempty"`
	Events    []json.RawMessage `json:"events,omitempty"`
	Reasoning string            `json:"reasoning,omitempty"`
	// Raw is the engine's JSON, including any part this type does not model.
	Raw json.RawMessage `json:"-"`
}

// VisionObject is a detected object. Coordinates are in a 0 to 1000 space
// over the image width and height; multiply x by width/1000 and y by
// height/1000 for pixels. Details not selected by Fields are empty.
type VisionObject struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// BBox is [x0, y0, x1, y1].
	BBox  []float64 `json:"bbox,omitempty"`
	Score float64   `json:"score,omitempty"`
	// Point is an [x, y] point on the object.
	Point []float64 `json:"point,omitempty"`
	Mask  *Mask     `json:"mask,omitempty"`
	// Position is a coarse placement, such as "left" or "top".
	Position string `json:"position,omitempty"`
	// Description holds attributes such as color and shape.
	Description map[string]string `json:"description,omitempty"`
}

// TextLine is a line of recognized text, with details as in VisionObject.
type TextLine struct {
	Text  string    `json:"string"`
	BBox  []float64 `json:"bbox,omitempty"`
	Score float64   `json:"score,omitempty"`
	Point []float64 `json:"point,omitempty"`
}

// Relation links two objects by ID, as in {1, "next_to", 2}.
type Relation struct {
	Subject   int    `json:"subject"`
	Predicate string `json:"predicate"`
	Object    int    `json:"object"`
}

// Mask is an object mask. Polygon formats fill Polygons, each a flattened
// x0, y0, x1, y1, ... outline in the 0 to 1000 space. MaskBitmap fills Size
// and Counts instead.
type Mask struct {
	Polygons [][]float64
	// Size is [height, width] in pixels.
	Size []int
	// Counts is an uncompressed COCO run-length encoding: alternating runs
	// of background and mask pixels in column-major order, starting with
	// background.
	Counts []int
}

// UnmarshalJSON accepts a flat polygon, a list of polygons or a run-length
// encoded bitmap.
func (m *Mask) UnmarshalJSON(data []byte) error {
	*m = Mask{}
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '{' {
		var bitmap struct {
			Size   []int `json:"size"`
			Counts []int `json:"counts"`
		}
		if err := json.Unmarshal(data, &bitmap); err != nil {
			return err
		}
		m.Size, m.Counts = bitmap.Size, bitmap.Counts
		return nil
	}
	var polygon []float64
	if err := json.Unmarshal(data, &polygon); err == nil {
		if len(polygon) > 0 {
			m.Polygons = [][]float64{polygon}
		}
		return nil
	}
	return json.Unmarshal(data, &m.Polygons)
}

// MarshalJSON writes a bitmap as {"size", "counts"} and polygons as a list
// of flattened polygons.
func (m Mask) MarshalJSON() ([]byte, error) {
	if m.Counts != nil {
		return json.Marshal(struct {
			Size   []int `json:"size"`
			Counts []int `json:"counts"`
		}{m.Size, m.Counts})
	}
	if m.Polygons == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(m.Polygons)
}

// Bitmap decodes a MaskBitmap mask, with 0xff for mask pixels. It returns nil
// for polygon masks or an inconsistent encoding.
func (m Mask) Bitmap() *image.Alpha {
	if len(m.Size) != 2 || m.Size[0] <= 0 || m.Size[1] <= 0 || m.Size[0] > math.MaxInt32/m.Size[1] {
		return nil
	}
	height, width := m.Size[0], m.Size[1]
	bitmap := image.NewAlpha(image.Rect(0, 0, width, height))
	position := 0
	for i, count := range m.Counts {
		if count < 0 || count > width*height-position {
			return nil
		}
		if i%2 == 1 {
			for p := position; p < position+count; p++ {
				bitmap.Pix[(p%height)*bitmap.Stride+p/height] = 0xff
			}
		}
		position += count
	}
	if position != width*height {
		return nil
	}
	return bitmap
}

// FormField is one field of an Extract form. The engine reads Schema, which
// ChoiceField, CountField and TextField build for the common kinds.
type FormField struct {
	Name   string
	Schema map[string]any
}

// Form is an ordered list of fields; the engine fills them in order.
type Form []FormField

// ChoiceField is a field whose value is one of choices, as {"enum": choices}.
func ChoiceField(name string, choices ...string) FormField {
	return FormField{Name: name, Schema: map[string]any{"enum": choices}}
}

// CountField is a bounded integer field, as {"type": "integer", "minimum",
// "maximum"}.
func CountField(name string, minimum, maximum int) FormField {
	return FormField{Name: name, Schema: map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}}
}

// TextField is a free text field of at most maxLength characters, as
// {"type": "string", "maxLength": maxLength}.
func TextField(name string, maxLength int) FormField {
	return FormField{Name: name, Schema: map[string]any{"type": "string", "maxLength": maxLength}}
}

// MarshalJSON writes the form as a JSON object in field order.
func (f Form) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for i, field := range f {
		if i > 0 {
			buffer.WriteByte(',')
		}
		name, err := json.Marshal(field.Name)
		if err != nil {
			return nil, err
		}
		schema, err := json.Marshal(field.Schema)
		if err != nil {
			return nil, fmt.Errorf("form field %q: %w", field.Name, err)
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		buffer.Write(schema)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// UnmarshalJSON reads a JSON object of field schemas, keeping field order.
func (f *Form) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return errors.New("cactus: form must be a JSON object of fields")
	}
	form := Form{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		field := FormField{Name: token.(string)}
		if err := decoder.Decode(&field.Schema); err != nil {
			return fmt.Errorf("cactus: form field %q: %w", field.Name, err)
		}
		form = append(form, field)
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*f = form
	return nil
}

func (f Form) validate() error {
	if len(f) == 0 {
		return errors.New("cactus: form has no fields")
	}
	seen := make(map[string]bool, len(f))
	for _, field := range f {
		if field.Name == "" || seen[field.Name] {
			return fmt.Errorf("cactus: form field name %q is empty or repeated", field.Name)
		}
		if field.Schema == nil {
			return fmt.Errorf("cactus: form field %q has no schema", field.Name)
		}
		seen[field.Name] = true
	}
	return nil
}

// FormValue is a filled form field.
type FormValue struct {
	// Value is a string for choice and text fields and a float64 for count
	// fields. It is nil when the model chose no value.
	Value any `json:"value"`
	// Probability is the confidence of a text value.
	Probability float64 `json:"probability,omitempty"`
	// Probabilities maps each choice or count to its probability; "-" is the
	// probability of no value.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// RGBImage is packed 8-bit RGB: Width*Height pixels in row-major order, three
// bytes per pixel, without row padding. Mirage passes Pix to the engine
// without converting it.
type RGBImage struct {
	Pix    []byte
	Width  int
	Height int
}

// ColorModel returns color.RGBAModel.
func (p *RGBImage) ColorModel() color.Model { return color.RGBAModel }

// Bounds returns the rectangle from (0, 0) to (Width, Height).
func (p *RGBImage) Bounds() image.Rectangle { return image.Rect(0, 0, p.Width, p.Height) }

// At returns the opaque pixel at (x, y), or transparent black outside the
// image or the pixel data.
func (p *RGBImage) At(x, y int) color.Color {
	if !image.Pt(x, y).In(p.Bounds()) {
		return color.RGBA{}
	}
	i := (y*p.Width + x) * 3
	if i+3 > len(p.Pix) {
		return color.RGBA{}
	}
	return color.RGBA{p.Pix[i], p.Pix[i+1], p.Pix[i+2], 0xff}
}

// DecodeImage decodes a PNG, JPEG, GIF (first frame) or any other format
// registered with the image package. A JPEG or PNG with an EXIF orientation
// other than 1 is rotated or flipped upright, as upstream does, and returned
// as an *RGBImage without alpha. It reads r to EOF.
func DecodeImage(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("cactus: read image: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cactus: decode image: %w", err)
	}
	if orientation := exifOrientation(data); orientation != 1 {
		rgb, width, height, err := packRGB(img)
		if err != nil {
			return nil, err
		}
		return orient(&RGBImage{Pix: rgb, Width: width, Height: height}, orientation), nil
	}
	return img, nil
}

// NewMirage loads vision weights without downloading or initializing text or
// speech weights.
func NewMirage(ctx context.Context, config MirageConfig) (*Mirage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	size := config.BufferSize
	if size == 0 {
		size = DefaultVisionBufferSize
	}
	if size < 2 || size > math.MaxInt32 {
		return nil, fmt.Errorf("cactus: invalid buffer size %d", size)
	}
	weightsPath := config.WeightsPath
	if weightsPath == "" {
		var err error
		weightsPath, err = fetchVisionWeights(ctx, FetchOptions{CacheDir: config.CacheDir})
		if err != nil {
			return nil, err
		}
	}
	weightsPath, err := filepath.Abs(weightsPath)
	if err != nil {
		return nil, fmt.Errorf("cactus: resolve vision weights path: %w", err)
	}
	libraryPath, err := resolveLibraryPath(ctx, Config{Generation: 3, LibraryPath: config.LibraryPath, CacheDir: config.CacheDir})
	if err != nil {
		return nil, err
	}
	m := &Mirage{runtime: runtimes[3], weightsPath: weightsPath, buffer: make([]byte, size)}
	m.runtime.mu.Lock()
	defer m.runtime.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.runtime.ensureLibraryLocked(libraryPath, 3); err != nil {
		return nil, err
	}
	if err := m.bindLocked(); err != nil {
		return nil, err
	}
	return m, nil
}

// bindLocked loads this Mirage's weights unless they are the loaded vision
// model. Text and speech models stay loaded. The caller must hold the runtime
// mutex.
func (m *Mirage) bindLocked() error {
	if m.closed {
		return ErrClosed
	}
	api := m.runtime.api
	if api == nil {
		return errors.New("cactus: engine is not loaded")
	}
	if m.runtime.visionWeights == m.weightsPath {
		return nil
	}
	blob, err := os.ReadFile(m.weightsPath)
	if err != nil {
		return fmt.Errorf("cactus: read vision weights: %w", err)
	}
	if weightsKind(blob) != modelVision {
		return errors.New("cactus: weights are not a recognized vision model")
	}
	m.runtime.visionWeights = ""
	if code := api.load(blob, uint64(len(blob))); code < 0 {
		return api.failure("load vision weights", code, "")
	}
	// The engine copies the archive, so blob is not retained.
	if api.models()&modelVision == 0 {
		return errors.New("cactus: engine did not load the vision model")
	}
	m.runtime.visionWeights = m.weightsPath
	return nil
}

// Close releases the response buffer. Later calls return ErrClosed. Loaded
// vision weights stay in the engine for other Mirage values.
func (m *Mirage) Close() error {
	m.runtime.mu.Lock()
	defer m.runtime.mu.Unlock()
	m.closed = true
	m.buffer = nil
	return nil
}

// Describe describes the scene, objects, text and relations in an image.
// Images other than *RGBImage are converted to packed RGB first, ignoring
// alpha. Calls are serialized with all text and speech native work. If the
// engine JSON does not fit Description, the error comes with a Description
// holding only Raw.
func (m *Mirage) Describe(ctx context.Context, img image.Image, options DescribeOptions) (Description, error) {
	data, err := m.describe(ctx, img, options, nil)
	if err != nil {
		return Description{}, err
	}
	var description Description
	if err := json.Unmarshal(data, &description); err != nil {
		return Description{Raw: data}, fmt.Errorf("cactus: decode description: %w", err)
	}
	description.Raw = data
	return description, nil
}

// Extract fills form from an image and returns the values by field name.
// Fields are filled in order, so a field can depend on earlier ones. Sections
// and Fields are passed to the engine but have no effect on forms.
func (m *Mirage) Extract(ctx context.Context, img image.Image, form Form, options DescribeOptions) (map[string]FormValue, error) {
	if err := form.validate(); err != nil {
		return nil, err
	}
	data, err := m.describe(ctx, img, options, form)
	if err != nil {
		return nil, err
	}
	var response struct {
		Form map[string]FormValue `json:"form"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("cactus: decode form: %w", err)
	}
	if response.Form == nil {
		return nil, errors.New("cactus: engine response has no form")
	}
	return response.Form, nil
}

func (m *Mirage) describe(ctx context.Context, img image.Image, options DescribeOptions, form Form) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err := options.encode(form)
	if err != nil {
		return nil, err
	}
	language, err := cString("vision language", options.Language, true)
	if err != nil {
		return nil, err
	}
	keywords, err := cString("vision keywords", strings.Join(options.Keywords, "\n"), true)
	if err != nil {
		return nil, err
	}
	rgb, width, height, err := packRGB(img)
	if err != nil {
		return nil, err
	}
	m.runtime.mu.Lock()
	defer m.runtime.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.bindLocked(); err != nil {
		return nil, err
	}
	api := m.runtime.api
	clear(m.buffer)
	code := api.describe(rgb, int32(width), int32(height), bytePointer(encoded), bytePointer(language), bytePointer(keywords), m.buffer, int32(len(m.buffer)))
	data, err := nativeOutput(api, "describe", code, m.buffer)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(data), nil
}

// describeKeys are the options set through typed DescribeOptions fields or
// Extract, which Extra cannot repeat. Language and keywords are separate
// cactus_describe arguments that the engine ignores inside the options.
var describeKeys = map[string]bool{
	"sections": true, "fields": true, "conf": true, "text_conf": true,
	"max_objects": true, "max_text_lines": true, "max_tokens": true,
	"mask_format": true, "nms": true, "form": true,
	"language": true, "keywords": true,
}

// encode returns the NUL-terminated options JSON for cactus_describe.
func (o DescribeOptions) encode(form Form) ([]byte, error) {
	options := make(map[string]any, len(o.Extra)+10)
	for key, value := range o.Extra {
		if describeKeys[key] {
			return nil, fmt.Errorf("cactus: set vision option %q with its DescribeOptions field or Extract", key)
		}
		options[key] = value
	}
	if o.Sections != nil {
		options["sections"] = o.Sections
	}
	if o.Fields != nil {
		options["fields"] = o.Fields
	}
	for _, threshold := range []struct {
		key   string
		value float64
	}{{"conf", o.Confidence}, {"text_conf", o.TextConfidence}} {
		if threshold.value == 0 {
			continue
		}
		if !(threshold.value > 0 && threshold.value <= 1) {
			return nil, fmt.Errorf("cactus: vision option %s must be in (0, 1]", threshold.key)
		}
		options[threshold.key] = threshold.value
	}
	for _, limit := range []struct {
		key   string
		value int
	}{{"max_objects", o.MaxObjects}, {"max_text_lines", o.MaxTextLines}, {"max_tokens", o.MaxTokens}} {
		if limit.value < 0 || limit.value > math.MaxInt32 {
			return nil, fmt.Errorf("cactus: invalid vision option %s %d", limit.key, limit.value)
		}
		if limit.value > 0 {
			options[limit.key] = limit.value
		}
	}
	if o.MaskFormat != "" {
		options["mask_format"] = o.MaskFormat
	}
	if o.NMS != nil {
		options["nms"] = *o.NMS
	}
	if form != nil {
		options["form"] = form
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("cactus: encode vision options: %w", err)
	}
	return cString("vision options", string(encoded), false)
}

// packRGB returns the image as packed RGB without alpha. *RGBImage pixels are
// used in place.
func packRGB(img image.Image) ([]byte, int, int, error) {
	if img == nil {
		return nil, 0, 0, errors.New("cactus: image is nil")
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, 0, 0, fmt.Errorf("cactus: image is %dx%d; want positive dimensions", width, height)
	}
	if height > math.MaxInt32/3/width {
		return nil, 0, 0, fmt.Errorf("cactus: image is %dx%d; want at most %d pixels", width, height, math.MaxInt32/3)
	}
	if src, ok := img.(*RGBImage); ok {
		if len(src.Pix) != width*height*3 {
			return nil, 0, 0, fmt.Errorf("cactus: RGB image has %d bytes; want %d for %dx%d", len(src.Pix), width*height*3, width, height)
		}
		return src.Pix, width, height, nil
	}
	rgb := make([]byte, 0, width*height*3)
	switch src := img.(type) {
	case *image.NRGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			row := src.Pix[src.PixOffset(bounds.Min.X, y):]
			for x := range width {
				rgb = append(rgb, row[x*4], row[x*4+1], row[x*4+2])
			}
		}
	case *image.RGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			row := src.Pix[src.PixOffset(bounds.Min.X, y):]
			for x := range width {
				pixel := row[x*4 : x*4+4]
				if pixel[3] == 0xff {
					rgb = append(rgb, pixel[0], pixel[1], pixel[2])
					continue
				}
				c := color.NRGBAModel.Convert(color.RGBA{pixel[0], pixel[1], pixel[2], pixel[3]}).(color.NRGBA)
				rgb = append(rgb, c.R, c.G, c.B)
			}
		}
	case *image.YCbCr:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				c := src.YCbCrAt(x, y)
				r, g, b := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
				rgb = append(rgb, r, g, b)
			}
		}
	case *image.Gray:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			row := src.Pix[src.PixOffset(bounds.Min.X, y):]
			for x := range width {
				rgb = append(rgb, row[x], row[x], row[x])
			}
		}
	default:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				rgb = append(rgb, c.R, c.G, c.B)
			}
		}
	}
	return rgb, width, height, nil
}
