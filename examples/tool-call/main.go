package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/zbiljic/cactus-go"
)

type weatherArguments struct {
	City string `json:"city" jsonschema:"description=City whose weather should be returned."`
}

type weatherResult struct {
	City        string `json:"city"`
	Temperature int    `json:"temperature_c"`
	Conditions  string `json:"conditions"`
}

func main() {
	ctx := context.Background()
	agent, err := cactus.New(ctx, cactus.Config{
		Tools: []cactus.Tool{weatherTool()},
	})
	if err != nil {
		log.Fatal(err)
	}

	response, err := agent.Run(
		ctx,
		"What's the weather like in Lagos right now?",
		cactus.DefaultMaxSteps,
		cactus.DefaultMaxNewTokens,
	)
	if err != nil {
		log.Fatal(err)
	}

	results, err := json.MarshalIndent(response.Results, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("results: %s\n", results)
	if response.Confidence != nil {
		fmt.Printf("confidence: %.2f\n", *response.Confidence)
	}
}

func weatherTool() cactus.Tool {
	return cactus.NewTool(
		"get_weather",
		"Get the current weather for a city.",
		func(_ context.Context, arguments weatherArguments) (weatherResult, error) {
			if arguments.City == "" {
				return weatherResult{}, errors.New("city is required")
			}
			return weatherResult{
				City:        arguments.City,
				Temperature: 27,
				Conditions:  "clear",
			}, nil
		},
	)
}
