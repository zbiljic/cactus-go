package main

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zbiljic/cactus-go"
)

type fakeMirage struct {
	images  []image.Image
	options []cactus.DescribeOptions
	forms   []cactus.Form
}

func (m *fakeMirage) Describe(_ context.Context, img image.Image, options cactus.DescribeOptions) (cactus.Description, error) {
	m.images, m.options = append(m.images, img), append(m.options, options)
	return cactus.Description{Raw: json.RawMessage(`{"scene":{"background":"white"}}`)}, nil
}

func (m *fakeMirage) Extract(_ context.Context, img image.Image, form cactus.Form, options cactus.DescribeOptions) (map[string]cactus.FormValue, error) {
	m.images, m.options, m.forms = append(m.images, img), append(m.options, options), append(m.forms, form)
	return map[string]cactus.FormValue{
		"z":     {Value: "red", Probabilities: map[string]float64{"red": 1}},
		"count": {Value: 2.0, Probabilities: map[string]float64{"2": 1}},
	}, nil
}

func writeTestPNG(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(1, 1, color.NRGBA{200, 10, 10, 0xff})
	path := filepath.Join(t.TempDir(), "image.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	app, stdout, _ := testApplication("")
	vision := &fakeMirage{}
	var config cactus.MirageConfig
	app.deps.newMirage = func(_ context.Context, c cactus.MirageConfig) (describer, error) {
		config = c
		return vision, nil
	}
	path := writeTestPNG(t)
	code := app.run(context.Background(), []string{
		"describe", "--weights", "mirage.cact", "--cache", "cache", "--sections", "objects, text", "--fields", "none",
		"--mask-format", "bitmap", "--conf", "0.4", "--text-conf", "0.3", "--max-objects", "3", "--max-text-lines", "2",
		"--nms=false", "--language", "de", "--keywords", "Paris,Ada",
		"--options", `{"keyword_bias":2}`, path,
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	nms := false
	want := cactus.DescribeOptions{
		Sections: []cactus.VisionSection{cactus.SectionObjects, cactus.SectionText}, Fields: []cactus.VisionField{},
		MaskFormat: cactus.MaskBitmap, Confidence: 0.4, TextConfidence: 0.3, MaxObjects: 3, MaxTextLines: 2, NMS: &nms,
		Language: "de", Keywords: []string{"Paris", "Ada"}, Extra: map[string]any{"keyword_bias": 2.0},
	}
	if config.WeightsPath != "mirage.cact" || config.CacheDir != "cache" || len(vision.options) != 1 || !reflect.DeepEqual(vision.options[0], want) {
		t.Fatalf("config=%+v options=%+v", config, vision.options)
	}
	if size := vision.images[0].Bounds().Size(); size != image.Pt(3, 2) {
		t.Fatalf("image size = %v", size)
	}
	if stdout.String() != `{"scene":{"background":"white"}}`+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDescribeForm(t *testing.T) {
	t.Parallel()
	app, stdout, _ := testApplication("")
	vision := &fakeMirage{}
	app.deps.newMirage = func(context.Context, cactus.MirageConfig) (describer, error) { return vision, nil }
	formPath := filepath.Join(t.TempDir(), "form.json")
	if err := os.WriteFile(formPath, []byte(`{"z":{"enum":["red","blue"]},"count":{"type":"integer","minimum":0,"maximum":4}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := app.run(context.Background(), []string{"describe", "--form", formPath, writeTestPNG(t)}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if len(vision.forms) != 1 || vision.forms[0][0].Name != "z" || vision.forms[0][1].Name != "count" || vision.options[0].Fields != nil {
		t.Fatalf("forms = %+v options=%+v", vision.forms, vision.options)
	}
	// Values print in form order.
	if want := `{"z":{"value":"red","probabilities":{"red":1}},"count":{"value":2,"probabilities":{"2":1}}}` + "\n"; stdout.String() != want {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDescribeUsageErrors(t *testing.T) {
	t.Parallel()
	path := writeTestPNG(t)
	for _, args := range [][]string{
		{"describe"},
		{"describe", path, path},
		{"describe", "--options", "[]", path},
		{"describe", "--options", "{", path},
		{"describe", "--nms", "maybe", path},
	} {
		app, _, stderr := testApplication("")
		app.deps.newMirage = func(context.Context, cactus.MirageConfig) (describer, error) {
			t.Fatal("Mirage loaded for invalid arguments")
			return nil, nil
		}
		if code := app.run(context.Background(), args); code != 2 || !strings.Contains(stderr.String(), "cactusz:") {
			t.Fatalf("%v: exit code = %d stderr=%q", args, code, stderr.String())
		}
	}
	app, _, stderr := testApplication("")
	notImage := filepath.Join(t.TempDir(), "image.txt")
	if err := os.WriteFile(notImage, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := app.run(context.Background(), []string{"describe", notImage}); code != 1 || !strings.Contains(stderr.String(), "decode image") {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}
}
