package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/zbiljic/cactus-go"
)

func main() {
	generation := flag.Int("generation", cactus.DefaultGeneration, "model generation (2 or 3)")
	platform := flag.String("platform", "", "target platform; empty selects the current platform")
	cacheDir := flag.String("cache", "", "engine cache directory")
	list := flag.Bool("list", false, "list supported platforms")
	flag.Parse()

	if *list {
		for _, supported := range cactus.SupportedPlatforms() {
			fmt.Println(supported)
		}
		return
	}

	path, err := cactus.FetchEngine(context.Background(), cactus.FetchOptions{
		Generation: *generation,
		Platform:   cactus.Platform(*platform),
		CacheDir:   *cacheDir,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(path)
}
