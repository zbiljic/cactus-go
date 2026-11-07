package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"

	"github.com/zbiljic/cactus-go"
)

// describer is the subset of *cactus.Mirage the describe command uses, so
// tests can substitute a fake.
type describer interface {
	Describe(ctx context.Context, img image.Image, options cactus.DescribeOptions) (cactus.Description, error)
	Extract(ctx context.Context, img image.Image, form cactus.Form, options cactus.DescribeOptions) (map[string]cactus.FormValue, error)
}

func (a *application) runDescribe(ctx context.Context, args []string) error {
	flags := a.flagSet(
		"Describe a PNG, JPEG or GIF image with Mirage and print the engine JSON, or fill a form from it.\n"+
			"JPEG and PNG photos are rotated upright by their EXIF orientation.",
		"describe [options] image",
	)
	weightsPath := flags.String("weights", "", "path to Mirage .cact weights; empty fetches the pinned model")
	libraryPath := flags.String("library", "", "path to the engine shared library")
	cacheDir := flags.String("cache", "", "engine cache directory")
	bufferSize := flags.Int("buffer-size", 0, "native response buffer size")
	formPath := flags.String("form", "", "path to a JSON object of form fields to fill, in order")
	sections := flags.String("sections", "", "comma-separated sections: scene, objects, regions, text, relations, events, reasoning")
	fields := flags.String("fields", "", "comma-separated details: bbox, score, point, mask, position, description; none for IDs and names only")
	maskFormat := flags.String("mask-format", "", "mask format: largest, merged, polygons or bitmap")
	confidence := flags.Float64("conf", 0, "object score threshold in (0, 1]")
	textConfidence := flags.Float64("text-conf", 0, "text line score threshold in (0, 1]")
	maxObjects := flags.Int("max-objects", 0, "object limit")
	maxTextLines := flags.Int("max-text-lines", 0, "text line limit")
	maxTokens := flags.Int("max-tokens", 0, "generated token limit")
	var nms *bool
	flags.Func("nms", "suppress overlapping detections: true or false; unset keeps the engine default", func(value string) error {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("want true or false")
		}
		nms = &enabled
		return nil
	})
	language := flags.String("language", "", "language of names and descriptions, such as de or fr")
	keywords := flags.String("keywords", "", "comma-separated words and phrases to favor")
	extra := flags.String("options", "", "other cactus_describe options as a JSON object")
	pretty := flags.Bool("pretty", false, "indent JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return usageError{errors.New("describe takes one image path")}
	}
	options := cactus.DescribeOptions{
		MaskFormat:     cactus.MaskFormat(*maskFormat),
		Confidence:     *confidence,
		TextConfidence: *textConfidence,
		MaxObjects:     *maxObjects,
		MaxTextLines:   *maxTextLines,
		MaxTokens:      *maxTokens,
		NMS:            nms,
		Language:       *language,
		Keywords:       splitList(*keywords),
	}
	for _, section := range splitList(*sections) {
		options.Sections = append(options.Sections, cactus.VisionSection(section))
	}
	if *fields == "none" {
		options.Fields = []cactus.VisionField{}
	} else {
		for _, field := range splitList(*fields) {
			options.Fields = append(options.Fields, cactus.VisionField(field))
		}
	}
	if *extra != "" {
		if err := json.Unmarshal([]byte(*extra), &options.Extra); err != nil || options.Extra == nil {
			return usageError{errors.New("--options must be a JSON object")}
		}
	}
	var form cactus.Form
	if *formPath != "" {
		data, err := readFileLimited(*formPath, maxInputSize)
		if err != nil {
			return fmt.Errorf("read form: %w", err)
		}
		if err := json.Unmarshal(data, &form); err != nil {
			return fmt.Errorf("decode form: %w", err)
		}
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	img, err := cactus.DecodeImage(file)
	file.Close()
	if err != nil {
		return err
	}
	vision, err := a.deps.newMirage(ctx, cactus.MirageConfig{
		WeightsPath: *weightsPath,
		LibraryPath: *libraryPath,
		CacheDir:    *cacheDir,
		BufferSize:  *bufferSize,
	})
	if err != nil {
		return fmt.Errorf("initialize Mirage: %w", err)
	}
	if form != nil {
		values, err := vision.Extract(ctx, img, form, options)
		if err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		// Print the values in form order.
		var buffer bytes.Buffer
		buffer.WriteByte('{')
		for i, field := range form {
			name, _ := json.Marshal(field.Name)
			value, err := json.Marshal(values[field.Name])
			if err != nil {
				return fmt.Errorf("encode form: %w", err)
			}
			if i > 0 {
				buffer.WriteByte(',')
			}
			buffer.Write(name)
			buffer.WriteByte(':')
			buffer.Write(value)
		}
		buffer.WriteByte('}')
		return writeJSON(a.stdout, json.RawMessage(buffer.Bytes()), *pretty)
	}
	description, err := vision.Describe(ctx, img, options)
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}
	return writeJSON(a.stdout, description.Raw, *pretty)
}

func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
