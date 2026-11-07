package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zbiljic/cactus-go"
)

type invoice struct {
	Vendor  string  `json:"vendor" jsonschema:"description=Company that issued the invoice."`
	Total   float64 `json:"total" jsonschema:"description=Total invoice amount."`
	DueDate string  `json:"due_date" jsonschema:"description=Payment due date in YYYY-MM-DD format."`
}

func main() {
	ctx := context.Background()
	agent, err := cactus.New(ctx, cactus.Config{
		Tools: []cactus.Tool{{Schema: cactus.SchemaFor[invoice](
			"invoice",
			"A purchase invoice extracted from text.",
		)}},
	})
	if err != nil {
		log.Fatal(err)
	}

	response, err := agent.Complete(
		ctx,
		"Invoice from Acme Corp, total 1200, due 2026-09-01.",
		cactus.DefaultMaxNewTokens,
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := cactus.ValidateResponse(response); err != nil {
		log.Fatal(err)
	}
	extracted, err := cactus.Extract[invoice](response)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("vendor: %s\n", extracted.Vendor)
	fmt.Printf("total: %.2f\n", extracted.Total)
	fmt.Printf("due: %s\n", extracted.DueDate)
	if response.Confidence != nil {
		fmt.Printf("confidence: %.2f\n", *response.Confidence)
	}
}
