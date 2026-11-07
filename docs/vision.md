# Vision

`NewMirage` loads the pinned ~21.6 MB `mirage.cact` archive from
[Cactus-Compute/mirage](https://huggingface.co/Cactus-Compute/mirage) and runs on
the Cactus engine. Vision weights are fetched only when requested; describing
images does not require text or speech weights.

## Describing images

```go
file, err := os.Open("photo.png")
if err != nil {
	log.Fatal(err)
}
img, err := cactus.DecodeImage(file)
file.Close()
if err != nil {
	log.Fatal(err)
}
vision, err := cactus.NewMirage(ctx, cactus.MirageConfig{})
if err != nil {
	log.Fatal(err)
}
description, err := vision.Describe(ctx, img, cactus.DescribeOptions{})
```

A `Description` has these sections:

- `Scene`: attributes such as `background`, `lighting` and `style`.
- `Objects`: each with an `ID`, a `Name` and the details listed below.
- `Text`: recognized lines.
- `Relations`: links between object IDs, such as `{2, "part_of", 5}`.
- `Reasoning`: the model's short rationale.
- `Regions` and `Events`: left as raw JSON.

`Raw` keeps the engine's JSON, including any part these types do not model.

Coordinates are in a 0 to 1000 space over the image width and height:

- `BBox` is `[x0, y0, x1, y1]`.
- `Point` is `[x, y]`.
- Polygon masks are flattened `x0, y0, x1, y1, ...` outlines.

Multiply x by `width/1000` and y by `height/1000` for pixels. `Score`,
`Position` (such as `left` or `top`) and `Description` (attributes such as
`color` and `shape`) complete each object.

## Options

`DescribeOptions` narrows the response. Zero values keep the engine defaults.

- `Sections`: the sections to return, such as `SectionObjects` and
  `SectionText`.
- `Fields`: the per-item details: `FieldBBox`, `FieldScore`, `FieldPoint`,
  `FieldMask`, `FieldPosition` and `FieldDescription`. By default every detail
  except `FieldPoint` is returned. An empty non-nil slice returns only IDs,
  names and text strings.
- `MaskFormat`:
  - `MaskLargest` and `MaskMerged` give one polygon.
  - `MaskPolygons` gives one polygon per part.
  - `MaskBitmap` gives COCO-style run-length encoding over the image's pixels,
    which `Mask.Bitmap` decodes to an `*image.Alpha`.
- `Confidence` and `TextConfidence`: score thresholds in `(0, 1]`.
- `MaxObjects`, `MaxTextLines` and `MaxTokens`: limits on the response.
- `NMS`: turns suppression of overlapping detections on or off.
- `Language`: names and descriptions in `de`, `fr`, `es`, `it`, `nl` or `pl`
  instead of English.
- `Keywords`: words and phrases to favor.
- `Extra`: passes other `cactus_describe` options, such as `keyword_bias`,
  `mask_threshold` or `mask_tolerance`, verbatim.

The engine validates names and ranges. It reports invalid options as a
`*NativeError` whose detail names the problem.

## Forms

`Extract` fills an ordered `Form` from an image and returns the values by field
name. Fields are filled in order, so later fields can depend on earlier ones.

```go
values, err := vision.Extract(ctx, img, cactus.Form{
	cactus.ChoiceField("type", "invoice", "receipt", "photo"),
	cactus.CountField("circles", 0, 5),
	cactus.TextField("title", 32),
}, cactus.DescribeOptions{})
fmt.Println(values["type"].Value, values["type"].Probabilities)
```

The field kinds return:

- `ChoiceField` and `CountField`: `Probabilities` for every option, plus `-`
  for no value.
- `TextField`: a single `Probability`.

`Value` is a string for choices and text and a float64 for counts. A choice
the model declines is `nil`; a declined text field is `"-"`. `FormField.Schema`
accepts other engine field schemas verbatim. A `Form` unmarshals from a JSON
object and keeps the file's field order, which a Go map would lose.

The published model answers best with field names that name a visible
property, such as `type` or `circle color`. Text fields often repeat the field
name or return `-`. Check the probabilities before acting on a value.

## Image input

`Describe` and `Extract` accept any `image.Image`. `*RGBImage` holds packed
8-bit RGB (`Width*Height*3` bytes, row-major, no padding). It is passed to the
engine without conversion. Other images are converted to RGB, and alpha is
dropped as Pillow's RGB conversion does: straight color is kept and nothing
is composited. Composite translucent images onto a background first if that
matters.

`DecodeImage` reads PNG, JPEG and GIF (first frame) with the standard library,
plus any format another import registers with the `image` package.

### EXIF orientation

The standard library ignores the EXIF `Orientation` tag. Many phone and camera
JPEGs store pixels sideways and record the rotation only in that tag. Upstream's
Python package applies it; cactus-go does not. Without it, Mirage sees the
sideways pixels. Text may not be read, and boxes and positions describe the
stored pixels rather than the upright photo.

For camera images, read the tag with an EXIF library of your choice and rotate
or flip the pixels upright before calling `Describe`. Alternatively, re-encode
the photos upright beforehand. Boxes are always relative to the pixels you
pass.

## Runtime and custom weights

Mirage shares one serialized Cactus engine runtime and library path with the
other models the engine runs, such as Needle 3 text and Whistle speech. The
engine holds one model of each kind, so they can all be loaded at once.

- Loading or switching vision weights preserves the loaded text and speech
  models and the active text conversation.
- Vision calls and vision weight loads during a live transcription leave the
  stream intact.
- An in-progress native call cannot be interrupted by context cancellation.

`MirageConfig.WeightsPath` selects a trusted custom vision archive. `LibraryPath`
and `CacheDir` work like `Config`, and `BufferSize` bounds the JSON response
(16 MiB by default). A response that does not fit returns a `*NativeError` that
reports the bytes needed.

The shared `.cact` format tag alone does not distinguish vision archives from
text or speech archives. Each loading path checks the recognized manifest layout
before loading, and wrong-kind or unknown-kind archives are rejected without
touching the engine. The vision classifier accepts the published 36-value
vision manifest; a future layout requires a library update. After loading,
`NewMirage` also confirms that the engine reports a vision model.

## Offline setup

`FetchMirage(ctx, cactus.FetchOptions{CacheDir: cacheDir})` prepares the engine
and vision weights for offline use, without fetching text or speech weights.
`Platform` selects the target desktop platform; `Generation` must be zero or 3.
Preserve the `.sha256` markers and use the same cache directory with `NewMirage`.

## Command line

`cactusz describe` prints the engine JSON for an image, or the filled form with
`--form form.json`:

```sh
go run ./cmd/cactusz describe --sections objects,text --pretty photo.png
go run ./cmd/cactusz describe --form form.json photo.png
```

See also the [image example](../examples/describe-image).
