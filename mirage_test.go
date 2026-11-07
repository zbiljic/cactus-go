package cactus

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// Metadata fixture only; native tests use the pinned published vision archive.
func testVisionArchive() []byte {
	blob := testHeadArchive(nil, false)
	const directory, index = 196 + 28*4, 13 + 2*27
	record := blob[directory+index*44:]
	record[0], record[1] = 2, 1
	binary.LittleEndian.PutUint32(record[4:], visionManifestLength)
	binary.LittleEndian.PutUint64(record[20:], uint64(len(blob)))
	binary.LittleEndian.PutUint64(record[28:], visionManifestLength*4)
	manifest := make([]byte, visionManifestLength*4)
	for i, value := range []float32{visionManifestMagic, 8192, 512, 5, 32, 64, 128, 256, 512} {
		binary.LittleEndian.PutUint32(manifest[i*4:], math.Float32bits(value))
	}
	blob = append(blob, manifest...)
	// The image encoder follows the manifest in the vision archive.
	binary.LittleEndian.PutUint32(blob[4:], index+2)
	return blob
}

func TestWeightsKindVision(t *testing.T) {
	t.Parallel()
	blob := testVisionArchive()
	if kind := weightsKind(blob); kind != modelVision {
		t.Fatalf("vision archive kind = %d", kind)
	}
	for size := range len(blob) {
		if weightsKind(blob[:size]) == modelVision {
			t.Fatalf("truncated vision archive of %d bytes accepted", size)
		}
	}
	for _, other := range [][]byte{testSpeechArchive(), testHeadArchive([]uint16{0x4000}, true)} {
		if weightsKind(other) == modelVision {
			t.Fatal("text or speech archive classified as vision")
		}
	}
	const directory, index = 196 + 28*4, 13 + 2*27
	for name, mutate := range map[string]func([]byte){
		"unknown manifest": func(b []byte) { binary.LittleEndian.PutUint32(b[len(b)-144:], math.Float32bits(4722)) },
		"other length":     func(b []byte) { binary.LittleEndian.PutUint32(b[directory+index*44+4:], 35) },
		"FP16 manifest":    func(b []byte) { b[directory+index*44] = 1 },
		"last tensor":      func(b []byte) { binary.LittleEndian.PutUint32(b[4:], index+1) },
	} {
		t.Run(name, func(t *testing.T) {
			b := append([]byte(nil), blob...)
			mutate(b)
			if kind := weightsKind(b); kind != 0 {
				t.Fatalf("invalid archive kind = %d", kind)
			}
		})
	}
}

func FuzzWeightsKindVision(f *testing.F) {
	f.Add(testVisionArchive())
	f.Fuzz(func(t *testing.T, blob []byte) { weightsKind(blob) })
}

// newTestMirage shares the fake runtime of a text agent. The engine reports
// the vision bit after any load.
func newTestMirage(t *testing.T, r *processRuntime) *Mirage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vision.cact")
	if err := os.WriteFile(path, testVisionArchive(), 0o600); err != nil {
		t.Fatal(err)
	}
	if r.api.models == nil {
		r.api.models = func() int32 { return modelText | modelVision }
	}
	if r.api.describe == nil {
		r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 {
			copy(out, `{"objects":[],"text":[]}`)
			return 0
		}
	}
	return &Mirage{runtime: r, weightsPath: path, buffer: make([]byte, 1<<12)}
}

func TestMirageDescribeAndIndependentWeights(t *testing.T) {
	t.Parallel()
	fake := &fakeNative{}
	a, r := newTestAgent(t, fake, Config{})
	r.speechWeights = "speech.cact"
	m := newTestMirage(t, r)
	pixels := &RGBImage{Pix: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}, Width: 3, Height: 2}
	r.api.describe = func(rgb []byte, width, height int32, options, language, keywords *byte, out []byte, capacity int32) int32 {
		if r.mu.TryLock() {
			r.mu.Unlock()
			t.Fatal("describe outside shared mutex")
		}
		if &rgb[0] != &pixels.Pix[0] || width != 3 || height != 2 || readCString(language) != "pl" || readCString(keywords) != "Łódź\ncafé" || capacity != 1<<12 {
			t.Fatal("incorrect describe arguments")
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(readCString(options)), &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"sections": []any{"objects", "text"}, "fields": []any{}, "mask_format": "bitmap", "conf": 0.5, "max_objects": 3.0, "nms": false, "mask_tolerance": 2.0}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("options = %v, want %v", got, want)
		}
		copy(out, `{"scene":{"background":"white"},"objects":[{"id":1,"name":"red square","bbox":[1,2,3,4],"score":0.8,"point":[2,3],"mask":{"size":[2,3],"counts":[1,2,3]},"position":"left","description":{"color":"red"}}],"text":[{"string":"OPEN","bbox":[5,6,7,8],"score":0.4}],"relations":[{"subject":1,"predicate":"next_to","object":2}],"regions":[{"kind":"sky"}],"events":[],"reasoning":"lang: pl","future":1}`)
		return 42 // Token count, not the length of the JSON.
	}
	nms := false
	description, err := m.Describe(context.Background(), pixels, DescribeOptions{
		Sections:   []VisionSection{SectionObjects, SectionText},
		Fields:     []VisionField{},
		Confidence: 0.5,
		MaxObjects: 3,
		MaskFormat: MaskBitmap,
		NMS:        &nms,
		Language:   "pl",
		Keywords:   []string{"Łódź", "café"},
		Extra:      map[string]any{"mask_tolerance": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	object := VisionObject{
		ID: 1, Name: "red square", BBox: []float64{1, 2, 3, 4}, Score: 0.8, Point: []float64{2, 3},
		Mask: &Mask{Size: []int{2, 3}, Counts: []int{1, 2, 3}}, Position: "left", Description: map[string]string{"color": "red"},
	}
	if description.Scene["background"] != "white" || !reflect.DeepEqual(description.Objects, []VisionObject{object}) ||
		!reflect.DeepEqual(description.Text, []TextLine{{Text: "OPEN", BBox: []float64{5, 6, 7, 8}, Score: 0.4}}) ||
		!reflect.DeepEqual(description.Relations, []Relation{{1, "next_to", 2}}) || string(description.Regions[0]) != `{"kind":"sky"}` ||
		description.Events == nil || description.Reasoning != "lang: pl" || !strings.Contains(string(description.Raw), `"future":1`) {
		t.Fatalf("Describe() = %+v", description)
	}
	if r.active != a || r.activeWeights != "" || r.speechWeights != "speech.cact" || fake.loads != 1 || len(fake.systems) != 1 || r.visionWeights != m.weightsPath {
		t.Fatal("vision load altered the text or speech model")
	}
	// A second vision model switches independently; a repeated call does not reload.
	r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 {
		copy(out, `{}`)
		return 0
	}
	second := newTestMirage(t, r)
	for _, vision := range []*Mirage{second, m, m} {
		if _, err := vision.Describe(context.Background(), pixels, DescribeOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if fake.loads != 3 || r.visionWeights != m.weightsPath || r.active != a {
		t.Fatalf("vision switching: loads=%d", fake.loads)
	}
}

func TestMirageRejectsWrongKindWithoutNativeLoad(t *testing.T) {
	t.Parallel()
	fake := &fakeNative{}
	a, r := newTestAgent(t, fake, Config{})
	m := newTestMirage(t, r)
	if _, err := m.Describe(context.Background(), &RGBImage{Pix: make([]byte, 3), Width: 1, Height: 1}, DescribeOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{testSpeechArchive(), testHeadArchive([]uint16{0x4000}, true)} {
		wrong := newTestMirage(t, r)
		if err := os.WriteFile(wrong.weightsPath, blob, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := wrong.Describe(context.Background(), &RGBImage{Pix: make([]byte, 3), Width: 1, Height: 1}, DescribeOptions{}); err == nil || !strings.Contains(err.Error(), "not a recognized vision model") {
			t.Fatalf("wrong-kind error = %v", err)
		}
	}
	wrongText, err := prepareAgent(Config{WeightsPath: m.weightsPath})
	if err != nil {
		t.Fatal(err)
	}
	wrongText.runtime = r
	if _, err := wrongText.Complete(context.Background(), "hello", 1); err == nil || !strings.Contains(err.Error(), "not a recognized text model") {
		t.Fatalf("vision-as-text error = %v", err)
	}
	wrongSpeech := newTestWhistle(t, r)
	wrongSpeech.weightsPath = m.weightsPath
	if _, err := wrongSpeech.Transcribe(context.Background(), nil, AudioOptions{}); err == nil || !strings.Contains(err.Error(), "not a recognized speech model") {
		t.Fatalf("vision-as-speech error = %v", err)
	}
	if fake.loads != 1 || r.active != a || r.visionWeights != m.weightsPath {
		t.Fatal("wrong-kind rejection mutated loaded models")
	}
}

func TestMirageLoadFailures(t *testing.T) {
	t.Parallel()
	fake := &fakeNative{}
	_, r := newTestAgent(t, fake, Config{})
	m := newTestMirage(t, r)
	image := &RGBImage{Pix: make([]byte, 3), Width: 1, Height: 1}
	// A load the engine accepts without reporting a vision model fails closed.
	r.api.models = func() int32 { return modelText }
	if _, err := m.Describe(context.Background(), image, DescribeOptions{}); err == nil || !strings.Contains(err.Error(), "did not load the vision model") {
		t.Fatalf("missing vision bit error = %v", err)
	}
	r.api.models = func() int32 { return modelText | modelVision }
	fake.loadCode = -3
	r.api.lastError = func() string { return "load detail" }
	var native *NativeError
	if _, err := m.Describe(context.Background(), image, DescribeOptions{}); !errors.As(err, &native) || native.Operation != "load vision weights" || native.Detail != "load detail" {
		t.Fatalf("load error = %v", err)
	}
	if r.visionWeights != "" {
		t.Fatal("failed load recorded vision weights")
	}
	// A description that does not fit the types still returns the JSON.
	fake.loadCode = 0
	r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 {
		copy(out, `{"scene":{"count":3}}`)
		return 0
	}
	if description, err := m.Describe(context.Background(), image, DescribeOptions{}); err == nil || string(description.Raw) != `{"scene":{"count":3}}` {
		t.Fatalf("undecodable description = %+v, %v", description, err)
	}
	r.api.describe = nil
	m = newTestMirage(t, r)
	fake.loadCode = 0
	if _, err := m.Describe(context.Background(), image, DescribeOptions{}); err != nil || r.visionWeights != m.weightsPath {
		t.Fatalf("retry after failed load: %v", err)
	}
}

func TestMirageInvalidInputAndErrors(t *testing.T) {
	t.Parallel()
	fake := &fakeNative{}
	_, r := newTestAgent(t, fake, Config{})
	m := newTestMirage(t, r)
	calls := 0
	r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 {
		calls++
		copy(out, `{}`)
		return 0
	}
	valid := &RGBImage{Pix: make([]byte, 3), Width: 1, Height: 1}
	nan := math.NaN()
	for name, test := range map[string]struct {
		img     image.Image
		options DescribeOptions
	}{
		"nil image":        {nil, DescribeOptions{}},
		"empty image":      {image.NewRGBA(image.Rect(0, 0, 0, 4)), DescribeOptions{}},
		"short RGB":        {&RGBImage{Pix: make([]byte, 5), Width: 1, Height: 2}, DescribeOptions{}},
		"long RGB":         {&RGBImage{Pix: make([]byte, 4), Width: 1, Height: 1}, DescribeOptions{}},
		"negative RGB":     {&RGBImage{Width: -1, Height: -1}, DescribeOptions{}},
		"huge image":       {&RGBImage{Width: 1 << 16, Height: 1 << 16}, DescribeOptions{}},
		"NaN confidence":   {valid, DescribeOptions{Confidence: nan}},
		"high confidence":  {valid, DescribeOptions{TextConfidence: 1.5}},
		"negative limit":   {valid, DescribeOptions{MaxTokens: -1}},
		"NaN extra":        {valid, DescribeOptions{Extra: map[string]any{"keyword_bias": nan}}},
		"typed extra":      {valid, DescribeOptions{Extra: map[string]any{"conf": 0.5}}},
		"form extra":       {valid, DescribeOptions{Extra: map[string]any{"form": map[string]any{}}}},
		"language extra":   {valid, DescribeOptions{Extra: map[string]any{"language": "de"}}},
		"keywords extra":   {valid, DescribeOptions{Extra: map[string]any{"keywords": []string{"a"}}}},
		"language NUL":     {valid, DescribeOptions{Language: "en\x00"}},
		"keyword NUL":      {valid, DescribeOptions{Keywords: []string{"a\x00"}}},
		"unencodable form": {valid, DescribeOptions{Extra: map[string]any{"x": func() {}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := m.Describe(context.Background(), test.img, test.options); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	for name, form := range map[string]Form{
		"nil":         nil,
		"empty":       {},
		"unnamed":     {{Schema: map[string]any{"enum": []string{"a"}}}},
		"repeated":    {ChoiceField("a", "x"), ChoiceField("a", "y")},
		"no schema":   {{Name: "a"}},
		"unencodable": {{Name: "a", Schema: map[string]any{"enum": math.Inf(1)}}},
	} {
		if _, err := m.Extract(context.Background(), valid, form, DescribeOptions{}); err == nil {
			t.Fatalf("%s form accepted", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Describe(ctx, valid, DescribeOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled describe = %v", err)
	}
	if calls != 0 || fake.loads != 0 {
		t.Fatal("invalid inputs reached the engine")
	}
	for _, output := range []string{"invalid JSON", strings.Repeat("x", 1<<12)} {
		r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 { copy(out, output); return 0 }
		if _, err := m.Describe(context.Background(), valid, DescribeOptions{}); err == nil {
			t.Fatal("invalid description accepted")
		}
	}
	r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 {
		copy(out, `{"objects":[]}`)
		return 0
	}
	if _, err := m.Extract(context.Background(), valid, Form{ChoiceField("a", "x")}, DescribeOptions{}); err == nil || !strings.Contains(err.Error(), "no form") {
		t.Fatalf("missing form error = %v", err)
	}
	r.api.lastError = func() string { return "invalid options: mask_format must be largest, merged, polygons or bitmap" }
	r.api.describe = func(_ []byte, _, _ int32, _, _, _ *byte, out []byte, _ int32) int32 {
		copy(out, `{"stale":true}`)
		return -1
	}
	var native *NativeError
	if _, err := m.Describe(context.Background(), valid, DescribeOptions{MaskFormat: "outline"}); !errors.As(err, &native) || native.Operation != "describe" || !strings.Contains(native.Detail, "mask_format") {
		t.Fatalf("native error = %v", err)
	}
	for _, size := range []int{1, int(math.MaxInt32) + 1} {
		if _, err := NewMirage(context.Background(), MirageConfig{BufferSize: size}); err == nil {
			t.Fatal("invalid buffer size accepted")
		}
	}
}

func TestMirageExtractKeepsFieldOrder(t *testing.T) {
	t.Parallel()
	fake := &fakeNative{}
	_, r := newTestAgent(t, fake, Config{})
	m := newTestMirage(t, r)
	r.api.describe = func(_ []byte, _, _ int32, options, _, _ *byte, out []byte, _ int32) int32 {
		const want = `{"form":{"color":{"enum":["red","blue"]},"legs":{"maximum":4,"minimum":0,"type":"integer"},"label":{"maxLength":12,"type":"string"},"z":{"type":"string","x-source":"color"}},"max_tokens":64}`
		if got := readCString(options); got != want {
			t.Fatalf("options = %s\nwant %s", got, want)
		}
		copy(out, `{"form":{"color":{"value":"red","probabilities":{"red":0.9,"blue":0.05,"-":0.05}},"legs":{"value":4,"probabilities":{"4":1}},"label":{"value":"OPEN","probability":0.6},"z":{"value":null,"probability":0.2}}}`)
		return 0
	}
	form := Form{
		ChoiceField("color", "red", "blue"),
		CountField("legs", 0, 4),
		TextField("label", 12),
		{Name: "z", Schema: map[string]any{"type": "string", "x-source": "color"}},
	}
	values, err := m.Extract(context.Background(), &RGBImage{Pix: make([]byte, 3), Width: 1, Height: 1}, form, DescribeOptions{MaxTokens: 64, Sections: nil})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FormValue{
		"color": {Value: "red", Probabilities: map[string]float64{"red": 0.9, "blue": 0.05, "-": 0.05}},
		"legs":  {Value: 4.0, Probabilities: map[string]float64{"4": 1}},
		"label": {Value: "OPEN", Probability: 0.6},
		"z":     {Value: nil, Probability: 0.2},
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("Extract() = %+v", values)
	}
	// A form read from JSON keeps the file's order.
	var decoded Form
	if err := json.Unmarshal([]byte(`{"z":{"type":"string","maxLength":3},"a":{"enum":["x","y"]}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil || string(encoded) != `{"z":{"maxLength":3,"type":"string"},"a":{"enum":["x","y"]}}` || decoded[0].Name != "z" {
		t.Fatalf("form round trip = %s, %v", encoded, err)
	}
	for _, invalid := range []string{`[]`, `null`, `{"a":1`, `{"a":[]}`} {
		if err := json.Unmarshal([]byte(invalid), &decoded); err == nil {
			t.Fatalf("form %s accepted", invalid)
		}
	}
}

func TestMirageClose(t *testing.T) {
	t.Parallel()
	fake := &fakeNative{}
	_, r := newTestAgent(t, fake, Config{})
	m := newTestMirage(t, r)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	valid := &RGBImage{Pix: make([]byte, 3), Width: 1, Height: 1}
	if _, err := m.Describe(context.Background(), valid, DescribeOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Describe() error = %v", err)
	}
	if _, err := m.Extract(context.Background(), valid, Form{ChoiceField("a", "x")}, DescribeOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Extract() error = %v", err)
	}
	if fake.loads != 0 {
		t.Fatal("closed Mirage loaded weights")
	}
}

func TestPackRGB(t *testing.T) {
	t.Parallel()
	want := []byte{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120}
	nrgba := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	rgba := image.NewRGBA(image.Rect(0, 0, 2, 2))
	paletted := image.NewPaletted(image.Rect(0, 0, 2, 2), nil)
	for i := range 4 {
		c := color.NRGBA{want[i*3], want[i*3+1], want[i*3+2], 0xff}
		nrgba.SetNRGBA(i%2, i/2, c)
		rgba.Set(i%2, i/2, c)
		paletted.Palette = append(paletted.Palette, c)
		paletted.SetColorIndex(i%2, i/2, uint8(i))
	}
	// Alpha is dropped; straight color is kept, as Pillow's RGB conversion does.
	translucent := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	translucent.SetNRGBA(0, 0, color.NRGBA{200, 100, 50, 0x80})
	premultiplied := image.NewRGBA(image.Rect(0, 0, 1, 1))
	premultiplied.Set(0, 0, color.NRGBA{200, 100, 50, 0x80})
	gray := image.NewGray(image.Rect(0, 0, 2, 1))
	gray.Pix = []byte{7, 9}
	ycbcr := image.NewYCbCr(image.Rect(0, 0, 1, 1), image.YCbCrSubsampleRatio444)
	ycbcr.Y[0], ycbcr.Cb[0], ycbcr.Cr[0] = color.RGBToYCbCr(200, 100, 50)
	r, g, b := color.YCbCrToRGB(ycbcr.Y[0], ycbcr.Cb[0], ycbcr.Cr[0])
	// A sub-image starts away from the origin of its parent.
	big := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 2 {
		for x := range 2 {
			big.SetNRGBA(x+1, y+2, nrgba.NRGBAAt(x, y))
		}
	}
	for name, test := range map[string]struct {
		img                  image.Image
		rgb                  []byte
		width, height        int
		sharesRGBImagePixels bool
	}{
		"NRGBA":         {nrgba, want, 2, 2, false},
		"RGBA":          {rgba, want, 2, 2, false},
		"paletted":      {paletted, want, 2, 2, false},
		"sub-image":     {big.SubImage(image.Rect(1, 2, 3, 4)), want, 2, 2, false},
		"translucent":   {translucent, []byte{200, 100, 50}, 1, 1, false},
		"premultiplied": {premultiplied, []byte{199, 99, 49}, 1, 1, false},
		"gray":          {gray, []byte{7, 7, 7, 9, 9, 9}, 2, 1, false},
		"YCbCr":         {ycbcr, []byte{r, g, b}, 1, 1, false},
		"RGBImage":      {&RGBImage{Pix: want, Width: 2, Height: 2}, want, 2, 2, true},
	} {
		t.Run(name, func(t *testing.T) {
			rgb, width, height, err := packRGB(test.img)
			if err != nil || !reflect.DeepEqual(rgb, test.rgb) || width != test.width || height != test.height {
				t.Fatalf("packRGB() = %v %dx%d, %v; want %v", rgb, width, height, err, test.rgb)
			}
			if test.sharesRGBImagePixels && unsafe.SliceData(rgb) != unsafe.SliceData(want) {
				t.Fatal("RGBImage pixels were copied")
			}
		})
	}
	rgb := &RGBImage{Pix: want, Width: 2, Height: 2}
	if rgb.At(1, 1) != (color.RGBA{100, 110, 120, 0xff}) || rgb.At(2, 0) != (color.RGBA{}) || rgb.ColorModel() != color.RGBAModel {
		t.Fatal("RGBImage pixel access")
	}
	if (&RGBImage{Pix: want[:5], Width: 2, Height: 2}).At(1, 1) != (color.RGBA{}) {
		t.Fatal("short RGBImage read past its pixels")
	}
}

func TestMaskJSONAndBitmap(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]Mask{
		`[1,2,3,4,5,6]`:                    {Polygons: [][]float64{{1, 2, 3, 4, 5, 6}}},
		`[]`:                               {},
		`[[1,2,3,4,5,6],[7,8,9,10,11,12]]`: {Polygons: [][]float64{{1, 2, 3, 4, 5, 6}, {7, 8, 9, 10, 11, 12}}},
		`{"size":[2,3],"counts":[1,2,3]}`:  {Size: []int{2, 3}, Counts: []int{1, 2, 3}},
	} {
		var mask Mask
		if err := json.Unmarshal([]byte(input), &mask); err != nil || !reflect.DeepEqual(mask, want) {
			t.Fatalf("%s decoded to %+v, %v", input, mask, err)
		}
	}
	for _, invalid := range []string{`"mask"`, `[1,"a"]`, `{"size":"x"}`} {
		var mask Mask
		if err := json.Unmarshal([]byte(invalid), &mask); err == nil {
			t.Fatalf("%s accepted", invalid)
		}
	}
	for mask, want := range map[*Mask]string{
		{Polygons: [][]float64{{1, 2, 3, 4}}}:    `[[1,2,3,4]]`,
		{Size: []int{1, 1}, Counts: []int{0, 1}}: `{"size":[1,1],"counts":[0,1]}`,
		{}:                                       `[]`,
	} {
		if encoded, err := json.Marshal(mask); err != nil || string(encoded) != want {
			t.Fatalf("Marshal(%+v) = %s, %v", *mask, encoded, err)
		}
	}
	// A 2x3 (height x width) mask in column-major runs: one background pixel,
	// three mask pixels, then two background pixels.
	bitmap := Mask{Size: []int{2, 3}, Counts: []int{1, 3, 2}}.Bitmap()
	if bitmap == nil || bitmap.Rect != image.Rect(0, 0, 3, 2) {
		t.Fatalf("Bitmap() = %+v", bitmap)
	}
	if got, want := bitmap.Pix, []byte{0, 0xff, 0, 0xff, 0xff, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Bitmap() pixels = %v, want %v", got, want)
	}
	for _, invalid := range []Mask{
		{Polygons: [][]float64{{1, 2}}},
		{Size: []int{2, 3}, Counts: []int{1, 3}},
		{Size: []int{2, 3}, Counts: []int{1, 6}},
		{Size: []int{2, 3}, Counts: []int{-1, 7}},
		{Size: []int{0, 3}, Counts: []int{}},
		{Size: []int{math.MaxInt32, math.MaxInt32}, Counts: []int{0}},
	} {
		if invalid.Bitmap() != nil {
			t.Fatalf("Bitmap() accepted %+v", invalid)
		}
	}
}

func TestFetchMirageUsesVerifiedCacheWithoutOtherWeights(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	platform := PlatformDarwinARM64
	for _, artifact := range []engineArtifact{artifacts[platform], visionWeights} {
		content := []byte("cached artifact " + artifact.libraryName)
		path := filepath.Join(cache, artifact.libraryName)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(content)
		if err := os.WriteFile(path+".sha256", []byte(fmt.Sprintf("%s\n%x\n", artifact.checksum, hash)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client := &http.Client{Transport: rejectCacheDownload{}}
	path, err := FetchMirage(context.Background(), FetchOptions{Platform: platform, CacheDir: cache, Client: client})
	if err != nil || path != filepath.Join(cache, artifacts[platform].libraryName) {
		t.Fatalf("offline Mirage path=%q err=%v", path, err)
	}
	for _, other := range []engineArtifact{baseWeights, speechWeights} {
		if _, err := os.Stat(filepath.Join(cache, other.libraryName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected %s: %v", other.libraryName, err)
		}
	}
	if _, err := FetchMirage(context.Background(), FetchOptions{Generation: 2}); !errors.Is(err, ErrUnsupportedGeneration) {
		t.Fatalf("Needle 2 vision error = %v", err)
	}
}
