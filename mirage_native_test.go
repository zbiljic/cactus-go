package cactus

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// testGlyphs is a 5x7 bitmap font for the characters testScene draws; the
// standard library has no font rasterizer.
var testGlyphs = map[rune][7]string{
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
	'N': {"#...#", "##..#", "#.#.#", "#.#.#", "#..##", "#...#", "#...#"},
	'2': {".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	'4': {"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	' ': {".....", ".....", ".....", ".....", ".....", ".....", "....."},
}

func drawText(img *image.RGBA, text string, x, y, scale int) {
	for _, r := range text {
		for row, line := range testGlyphs[r] {
			for column, pixel := range line {
				if pixel == '#' {
					fill(img, image.Rect(x+column*scale, y+row*scale, x+(column+1)*scale, y+(row+1)*scale), color.RGBA{0, 0, 0, 0xff})
				}
			}
		}
		x += 6 * scale
	}
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// Drawn shapes of testScene, in pixels.
var (
	testSceneSize   = image.Rect(0, 0, 512, 384)
	testRedSquare   = image.Rect(48, 160, 208, 320)
	testBlueCenter  = image.Pt(384, 240)
	testBlueRadius  = 80
	testSceneString = "OPEN 24"
)

// testScene draws text above a red square and a blue circle on white.
func testScene() *image.RGBA {
	img := image.NewRGBA(testSceneSize)
	fill(img, img.Bounds(), color.RGBA{0xff, 0xff, 0xff, 0xff})
	drawText(img, testSceneString, 48, 40, 8)
	fill(img, testRedSquare, color.RGBA{0xdc, 0x14, 0x14, 0xff})
	for y := testBlueCenter.Y - testBlueRadius; y < testBlueCenter.Y+testBlueRadius; y++ {
		for x := testBlueCenter.X - testBlueRadius; x < testBlueCenter.X+testBlueRadius; x++ {
			if dx, dy := x-testBlueCenter.X, y-testBlueCenter.Y; dx*dx+dy*dy < testBlueRadius*testBlueRadius {
				img.SetRGBA(x, y, color.RGBA{0x14, 0x28, 0xdc, 0xff})
			}
		}
	}
	return img
}

// pixelBox converts a 0-1000 box to pixels of a size.
func pixelBox(box []float64, size image.Rectangle) image.Rectangle {
	scale := func(value float64, extent int) int { return int(math.Round(value * float64(extent) / 1000)) }
	return image.Rect(scale(box[0], size.Dx()), scale(box[1], size.Dy()), scale(box[2], size.Dx()), scale(box[3], size.Dy()))
}

func intersectionOverUnion(a, b image.Rectangle) float64 {
	intersection := a.Intersect(b)
	area := func(r image.Rectangle) int { return r.Dx() * r.Dy() }
	return float64(area(intersection)) / float64(area(a)+area(b)-area(intersection))
}

func findObject(objects []VisionObject, word string) *VisionObject {
	for i := range objects {
		if strings.Contains(objects[i].Name, word) {
			return &objects[i]
		}
	}
	return nil
}

func TestNativeMirage(t *testing.T) {
	if os.Getenv("CACTUS_TEST_NATIVE") != "1" {
		t.Skip("set CACTUS_TEST_NATIVE=1 to test native vision")
	}
	ctx := context.Background()
	vision, err := NewMirage(ctx, MirageConfig{})
	if err != nil {
		t.Fatal(err)
	}
	r := vision.runtime
	visionBlob, err := os.ReadFile(r.visionWeights)
	if err != nil {
		t.Fatal(err)
	}
	if weightsKind(visionBlob) != modelVision || r.api.models()&modelVision == 0 {
		t.Fatal("published vision archive kind not recognized or not loaded")
	}

	// Decode the scene from PNG, as a caller reading a file would.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, testScene()); err != nil {
		t.Fatal(err)
	}
	img, err := DecodeImage(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	description, err := vision.Describe(ctx, img, DescribeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("description: %s", description.Raw)
	if description.Scene["background"] != "white" || description.Reasoning == "" {
		t.Fatalf("scene=%v reasoning=%q", description.Scene, description.Reasoning)
	}
	red, blue := findObject(description.Objects, "square"), findObject(description.Objects, "circle")
	if red == nil || blue == nil || red.Description["color"] != "red" || blue.Description["color"] != "blue" {
		t.Fatalf("objects = %+v", description.Objects)
	}
	for _, check := range []struct {
		object *VisionObject
		want   image.Rectangle
	}{{red, testRedSquare}, {blue, image.Rectangle{Min: testBlueCenter, Max: testBlueCenter}.Inset(-testBlueRadius)}} {
		object := check.object
		if len(object.BBox) != 4 || object.Score <= 0 || object.Point != nil || object.Mask == nil || len(object.Mask.Polygons) != 1 || object.Position == "" {
			t.Fatalf("object details = %+v", object)
		}
		if iou := intersectionOverUnion(pixelBox(object.BBox, testSceneSize), check.want); iou < 0.8 {
			t.Fatalf("%s box %v covers %v with IoU %.2f", object.Name, pixelBox(object.BBox, testSceneSize), check.want, iou)
		}
	}
	if !slices.ContainsFunc(description.Text, func(line TextLine) bool {
		return strings.Contains(strings.ToUpper(line.Text), "OPEN") && len(line.BBox) == 4
	}) {
		t.Fatalf("text = %+v", description.Text)
	}

	// Packed RGB reaches the engine unchanged and gives the same answer.
	scene := testScene()
	rgb := &RGBImage{Width: scene.Rect.Dx(), Height: scene.Rect.Dy()}
	for i := 0; i < len(scene.Pix); i += 4 {
		rgb.Pix = append(rgb.Pix, scene.Pix[i:i+3]...)
	}
	again, err := vision.Describe(ctx, rgb, DescribeOptions{})
	if err != nil || !bytes.Equal(again.Raw, description.Raw) {
		t.Fatalf("RGB description differs: %s err=%v", again.Raw, err)
	}
	// A PNG stored sideways with EXIF orientation 6 decodes upright.
	sideways := orient(rgb, 8)
	stored := image.NewNRGBA(image.Rect(0, 0, sideways.Width, sideways.Height))
	for i := range sideways.Width * sideways.Height {
		copy(stored.Pix[i*4:], sideways.Pix[i*3:i*3+3])
		stored.Pix[i*4+3] = 0xff
	}
	upright, err := DecodeImage(bytes.NewReader(withPNGExif(t, stored, testTIFF(binary.BigEndian, 3, 6))))
	if err != nil {
		t.Fatal(err)
	}
	if rotated, err := vision.Describe(ctx, upright, DescribeOptions{}); err != nil || !bytes.Equal(rotated.Raw, description.Raw) {
		t.Fatalf("EXIF-rotated description differs: %s err=%v", rotated.Raw, err)
	}

	// A bitmap mask decodes onto the drawn square.
	masks, err := vision.Describe(ctx, img, DescribeOptions{
		Sections:   []VisionSection{SectionObjects},
		Fields:     []VisionField{FieldMask, FieldPoint},
		MaskFormat: MaskBitmap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if masks.Scene != nil || masks.Text != nil || len(masks.Objects) == 0 || masks.Objects[0].BBox != nil || len(masks.Objects[0].Point) != 2 {
		t.Fatalf("sections and fields were not applied: %s", masks.Raw)
	}
	square := findObject(masks.Objects, "square")
	if square == nil || square.Mask == nil || !image.Pt(int(square.Point[0]*512/1000), int(square.Point[1]*384/1000)).In(testRedSquare) {
		t.Fatalf("bitmap objects = %s", masks.Raw)
	}
	bitmap := square.Mask.Bitmap()
	if bitmap == nil || bitmap.Rect != testSceneSize {
		t.Fatalf("bitmap mask = %+v", square.Mask)
	}
	inside, outside := 0, 0
	for y := range testSceneSize.Dy() {
		for x := range testSceneSize.Dx() {
			if bitmap.AlphaAt(x, y).A != 0 {
				if image.Pt(x, y).In(testRedSquare) {
					inside++
				} else {
					outside++
				}
			}
		}
	}
	if area := testRedSquare.Dx() * testRedSquare.Dy(); float64(inside) < 0.9*float64(area) || float64(outside) > 0.1*float64(area) {
		t.Fatalf("bitmap mask covers %d of %d square pixels and %d outside", inside, area, outside)
	}

	// A form is filled in order with constrained values.
	lone := image.NewRGBA(image.Rect(0, 0, 256, 256))
	fill(lone, lone.Rect, color.RGBA{0xff, 0xff, 0xff, 0xff})
	fill(lone, image.Rect(64, 64, 192, 192), color.RGBA{0xdc, 0x14, 0x14, 0xff})
	form := Form{
		ChoiceField("color", "red", "green", "blue"),
		CountField("count", 0, 5),
		ChoiceField("shape", "circle", "square", "triangle"),
	}
	values, err := vision.Extract(ctx, lone, form, DescribeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("form: %+v", values)
	// The model may decline a choice ("-"), which leaves Value nil.
	shape := values["shape"]
	if values["color"].Value != "red" || values["count"].Value != 1.0 || len(values) != 3 ||
		(shape.Value != nil && !slices.Contains([]any{"circle", "square", "triangle"}, shape.Value)) || shape.Probabilities["-"] == 0 {
		t.Fatalf("form = %+v", values)
	}
	for name, value := range values {
		total := 0.0
		for _, probability := range value.Probabilities {
			total += probability
		}
		if math.Abs(total-1) > 0.01 {
			t.Fatalf("%s probabilities sum to %f", name, total)
		}
	}

	// Engine diagnostics surface as native errors.
	var native *NativeError
	for options, detail := range map[*DescribeOptions]string{
		{MaskFormat: "outline"}: "mask_format",
		{Language: "xx"}:        "unsupported language",
	} {
		if _, err := vision.Describe(ctx, img, *options); !errors.As(err, &native) || !strings.Contains(native.Detail, detail) {
			t.Fatalf("options %+v error = %v", *options, err)
		}
	}
	small, err := NewMirage(ctx, MirageConfig{BufferSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := small.Describe(ctx, img, DescribeOptions{}); !errors.As(err, &native) || !strings.Contains(native.Detail, "too small") {
		t.Fatalf("small buffer error = %v", err)
	}
	german, err := vision.Describe(ctx, img, DescribeOptions{Language: "de", Sections: []VisionSection{SectionObjects}, Fields: []VisionField{}})
	if err != nil || findObject(german.Objects, "square") != nil || len(german.Objects) == 0 {
		t.Fatalf("German objects = %s, %v", german.Raw, err)
	}

	// Text and speech share the engine with vision: models=7. Loading and
	// running vision preserves a pending text tool call.
	type weatherArguments struct {
		City string `json:"city"`
	}
	a, err := New(ctx, Config{Tools: []Tool{{Schema: SchemaFor[weatherArguments]("get_weather", "Current weather for a city.")}}})
	if err != nil {
		t.Fatal(err)
	}
	speech, err := NewWhistle(ctx, WhistleConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if models := r.api.models(); models != modelText|modelSpeech|modelVision {
		t.Fatalf("models = %d", models)
	}
	visionPath := filepath.Join(t.TempDir(), "vision.cact")
	if err := os.WriteFile(visionPath, visionBlob, 0o600); err != nil {
		t.Fatal(err)
	}
	var baseline Response
	for _, interrupt := range []bool{false, true} {
		if err := a.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		initial, err := a.Complete(ctx, "what is the weather in Paris?", DefaultMaxNewTokens)
		if err != nil || len(initial.FunctionCalls) != 1 || initial.FunctionCalls[0].Name != "get_weather" {
			t.Fatalf("initial call=%+v err=%v", initial, err)
		}
		if interrupt {
			other, err := NewMirage(ctx, MirageConfig{WeightsPath: visionPath})
			if err != nil {
				t.Fatal(err)
			}
			if r.visionWeights != visionPath {
				t.Fatal("vision weights were not reloaded")
			}
			if _, err := other.Describe(ctx, img, DescribeOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		final, err := a.Complete(ctx, `[{"city":"Paris","weather":"rain, 14C"}]`, DefaultMaxNewTokens)
		if err != nil || !final.Success || final.Type != ResponseRespond {
			t.Fatalf("continuation=%+v err=%v", final, err)
		}
		if interrupt && (final.Type != baseline.Type || final.Reasoning != baseline.Reasoning || !reflect.DeepEqual(final.FunctionCalls, baseline.FunctionCalls) || !reflect.DeepEqual(final.Confidence, baseline.Confidence)) {
			t.Fatalf("vision changed conversation: got=%+v baseline=%+v", final, baseline)
		}
		baseline = final
	}
	if models := r.api.models(); models != modelText|modelSpeech|modelVision {
		t.Fatalf("models after reload = %d", models)
	}
	if transcript, err := speech.Transcribe(ctx, make([]float32, SpeechSampleRate), AudioOptions{}); err != nil || transcript.Text != "" {
		t.Fatalf("speech after vision=%+v err=%v", transcript, err)
	}
	// Vision after text and speech switches still answers the same.
	if after, err := vision.Describe(ctx, img, DescribeOptions{}); err != nil || !bytes.Equal(after.Raw, description.Raw) {
		t.Fatalf("vision after text and speech: %s err=%v", after.Raw, err)
	}
	// A vision call during live transcription leaves the stream intact.
	stream, err := speech.Stream(AudioOptions{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Process(ctx, make([]float32, SpeechSampleRate)); err != nil {
		t.Fatal(err)
	}
	if _, err := vision.Describe(ctx, img, DescribeOptions{Sections: []VisionSection{SectionScene}}); err != nil {
		t.Fatal(err)
	}
	// Reloading vision weights during the stream leaves it intact as well;
	// the engine copies archives, so a collection cannot free loaded weights.
	reloaded, err := NewMirage(ctx, MirageConfig{WeightsPath: visionPath})
	if err != nil || r.visionWeights != visionPath {
		t.Fatalf("vision reload during stream: %v", err)
	}
	runtime.GC()
	if after, err := reloaded.Describe(ctx, img, DescribeOptions{}); err != nil || !bytes.Equal(after.Raw, description.Raw) {
		t.Fatalf("vision after reload during stream: %s err=%v", after.Raw, err)
	}
	if step, err := stream.Process(ctx, make([]float32, SpeechSampleRate)); err != nil || step.Received < 1.9 {
		t.Fatalf("stream after vision=%+v err=%v", step, err)
	}
	if _, err := stream.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// Wrong-kind archives are rejected before loading, in every direction.
	if _, err := New(ctx, Config{WeightsPath: visionPath}); err == nil || !strings.Contains(err.Error(), "not a recognized text model") {
		t.Fatalf("vision as text: %v", err)
	}
	if _, err := NewWhistle(ctx, WhistleConfig{WeightsPath: visionPath}); err == nil || !strings.Contains(err.Error(), "not a recognized speech model") {
		t.Fatalf("vision as speech: %v", err)
	}
	for _, path := range []string{r.activeWeights, r.speechWeights} {
		if _, err := NewMirage(ctx, MirageConfig{WeightsPath: path}); err == nil || !strings.Contains(err.Error(), "not a recognized vision model") {
			t.Fatalf("%s as vision: %v", path, err)
		}
	}
	if models := r.api.models(); models != modelText|modelSpeech|modelVision {
		t.Fatalf("models after rejections = %d", models)
	}
	if response, err := a.Complete(ctx, "hello", DefaultMaxNewTokens); err != nil || !response.Success {
		t.Fatalf("text after vision=%+v err=%v", response, err)
	}
}
