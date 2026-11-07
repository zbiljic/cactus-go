// Describe a PNG, JPEG or GIF with Mirage, or fill a form from it with
// -form, a JSON object of field schemas in the order to fill them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/zbiljic/cactus-go"
)

func main() {
	formPath := flag.String("form", "", "JSON form to fill, such as {\"color\":{\"enum\":[\"red\",\"blue\"]}}")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatal("usage: go run ./examples/describe-image [-form form.json] image.png")
	}
	file, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	// The standard library ignores EXIF orientation: rotate camera photos
	// upright before describing them.
	img, err := cactus.DecodeImage(file)
	file.Close()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	vision, err := cactus.NewMirage(ctx, cactus.MirageConfig{})
	if err != nil {
		log.Fatal(err)
	}
	if *formPath != "" {
		data, err := os.ReadFile(*formPath)
		if err != nil {
			log.Fatal(err)
		}
		var form cactus.Form
		if err := json.Unmarshal(data, &form); err != nil {
			log.Fatal(err)
		}
		values, err := vision.Extract(ctx, img, form, cactus.DescribeOptions{})
		if err != nil {
			log.Fatal(err)
		}
		for _, field := range form {
			fmt.Printf("%s: %v\n", field.Name, values[field.Name].Value)
		}
		return
	}
	description, err := vision.Describe(ctx, img, cactus.DescribeOptions{
		Fields: []cactus.VisionField{cactus.FieldBBox, cactus.FieldScore, cactus.FieldDescription},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("scene: %v\n", description.Scene)
	// Boxes are in a 0-1000 space; scale them to pixels.
	size := img.Bounds().Size()
	pixels := func(box []float64) [4]int {
		if len(box) != 4 {
			return [4]int{}
		}
		return [4]int{
			int(box[0] * float64(size.X) / 1000), int(box[1] * float64(size.Y) / 1000),
			int(box[2] * float64(size.X) / 1000), int(box[3] * float64(size.Y) / 1000),
		}
	}
	for _, object := range description.Objects {
		fmt.Printf("object %d %q at %v score %.2f %v\n", object.ID, object.Name, pixels(object.BBox), object.Score, object.Description)
	}
	for _, line := range description.Text {
		fmt.Printf("text %q at %v\n", line.Text, pixels(line.BBox))
	}
	for _, relation := range description.Relations {
		fmt.Printf("relation %d %s %d\n", relation.Subject, relation.Predicate, relation.Object)
	}
}
