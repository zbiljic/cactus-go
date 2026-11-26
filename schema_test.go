package cactus

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaFor(t *testing.T) {
	t.Parallel()

	schema := SchemaFor[typedToolArguments]("weather", "Get the weather.")
	if schema.Name != "weather" || schema.Description != "Get the weather." {
		t.Fatalf("schema = %#v", schema)
	}
	var parameters map[string]any
	if err := json.Unmarshal(schema.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	if got := parameters["type"]; got != "object" {
		t.Fatalf("parameter type = %#v, want object", got)
	}
	for _, key := range []string{"$schema", "$id", "$ref", "$defs"} {
		if _, exists := parameters[key]; exists {
			t.Fatalf("parameters contain %q: %#v", key, parameters[key])
		}
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", parameters["properties"])
	}
	city, ok := properties["city"].(map[string]any)
	if !ok || city["type"] != "string" || city["description"] != "City whose weather should be returned." {
		t.Fatalf("city schema = %#v", properties["city"])
	}
	units, ok := properties["units"].(map[string]any)
	if !ok || !reflect.DeepEqual(units["enum"], []any{"celsius", "fahrenheit"}) {
		t.Fatalf("units schema = %#v", properties["units"])
	}
	if got, want := parameters["required"], []any{"city"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %#v, want %#v", got, want)
	}
}

// TestSchemaForMatchesUpstreamLayout pins SchemaFor to upstream's schema
// layout, since the model reads key order.
func TestSchemaForMatchesUpstreamLayout(t *testing.T) {
	t.Parallel()

	type controlLights struct {
		Room              string  `json:"room" jsonschema:"enum=kitchen,enum=living_room,enum=bedroom,enum=study" jsonschema_description:"The room to control."`
		Action            string  `json:"action" jsonschema:"enum=on,enum=off,enum=dim" jsonschema_description:"on, off, or dim."`
		BrightnessPercent *int    `json:"brightness_percent,omitempty" jsonschema:"minimum=0,maximum=100" jsonschema_description:"Brightness from 0 to 100."`
		Color             *string `json:"color,omitempty" jsonschema:"enum=warm white,enum=red" jsonschema_description:"The light color."`
		Tags              []struct {
			Label string `json:"label" jsonschema:"minLength=1,maxLength=20" jsonschema_description:"A tag."`
		} `json:"tags,omitempty" jsonschema:"minItems=1" jsonschema_description:"Tags to apply."`
	}
	want := `{"type":"object","properties":{` +
		`"room":{"type":"string","enum":["kitchen","living_room","bedroom","study"],"description":"The room to control."},` +
		`"action":{"type":"string","enum":["on","off","dim"],"description":"on, off, or dim."},` +
		`"brightness_percent":{"type":"integer","description":"Brightness from 0 to 100.","minimum":0,"maximum":100},` +
		`"color":{"type":"string","enum":["warm white","red"],"description":"The light color."},` +
		`"tags":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string","description":"A tag.","minLength":1,"maxLength":20}},"required":["label"]},"description":"Tags to apply.","minItems":1}` +
		`},"required":["room","action"]}`
	if got := string(SchemaFor[controlLights]("control_lights", "Set lights.").Parameters); got != want {
		t.Fatalf("parameters =\n%s\nwant\n%s", got, want)
	}
}

func TestOrderSchemaKeepsNamesAndValues(t *testing.T) {
	t.Parallel()

	got, err := orderSchema([]byte(`{"properties":{"z":{"description":"Z.","type":"string","default":"b"},"a":{"anyOf":[{"maxLength":3,"type":"string"},{"type":"null"}]}},"type":"object","$defs":{"Y":{"required":["k"],"type":"object"}},"examples":[{"z":"q","a":"p"}]}`))
	want := `{"type":"object","properties":{"z":{"type":"string","description":"Z.","default":"b"},"a":{"anyOf":[{"type":"string","maxLength":3},{"type":"null"}]}},"$defs":{"Y":{"type":"object","required":["k"]}},"examples":[{"z":"q","a":"p"}]}`
	if err != nil || string(got) != want {
		t.Fatalf("orderSchema() = %s, %v\nwant %s", got, err, want)
	}
}

func TestSchemaForKeepsFieldOrder(t *testing.T) {
	t.Parallel()

	type arguments struct {
		Zone   string `json:"zone"`
		Action string `json:"action"`
		Bright int    `json:"bright,omitempty"`
	}
	schema := SchemaFor[arguments]("lights", "Set lights.")
	text := string(schema.Parameters)
	zone, action, bright := strings.Index(text, `"zone"`), strings.Index(text, `"action"`), strings.Index(text, `"bright"`)
	if zone < 0 || !(zone < action && action < bright) {
		t.Fatalf("parameters = %s, want zone, action, bright in order", text)
	}
}
