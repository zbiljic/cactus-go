package cactus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/invopop/jsonschema"
)

// SchemaFor builds a tool schema whose parameters are derived from T. JSON
// field names and optionality follow encoding/json tags; JSON Schema
// annotations can be supplied with jsonschema tags. Properties keep the
// struct's field order, and every schema lists its keys in the order the
// model was trained on: type, properties and required, then enum or items,
// description and the constraints.
func SchemaFor[T any](name, description string) ToolSchema {
	return ToolSchema{
		Name:        name,
		Description: description,
		Parameters:  parametersFor[T](),
	}
}

func parametersFor[T any]() json.RawMessage {
	reflector := jsonschema.Reflector{
		Anonymous:                 true,
		DoNotReference:            true,
		ExpandedStruct:            true,
		AllowAdditionalProperties: true,
	}
	reflected := reflector.ReflectFromType(reflect.TypeFor[T]())
	reflected.Version = ""

	data, err := json.Marshal(reflected)
	if err == nil {
		data, err = orderSchema(data)
	}
	if err != nil {
		panic(fmt.Errorf("cactus: encode schema for %s: %w", reflect.TypeFor[T](), err))
	}
	return data
}

// schemaKeyOrder is the key order of upstream's tool schemas. Keys not listed
// follow in their original order.
var schemaKeyOrder = []string{
	"type", "properties", "required", "enum", "items", "description",
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern", "format", "minItems", "maxItems",
	"uniqueItems", "const",
}

// schemaMaps hold named subschemas; schemaLists hold lists of subschemas.
var (
	schemaMaps  = []string{"properties", "$defs", "definitions", "patternProperties"}
	schemaLists = []string{"anyOf", "oneOf", "allOf", "prefixItems"}
	schemaNodes = []string{"items", "additionalProperties", "not", "if", "then", "else", "contains"}
)

// orderedObject is a JSON object that keeps its key order.
type orderedObject []orderedMember

type orderedMember struct {
	key   string
	value any
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, member := range o {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, err := json.Marshal(member.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(member.value)
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// orderSchema rewrites a JSON Schema with every schema's keys in
// schemaKeyOrder, keeping the order of property names and values.
func orderSchema(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeOrdered(decoder)
	if err != nil {
		return nil, err
	}
	return json.Marshal(reorderSchema(value))
}

func decodeOrdered(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		var object orderedObject
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeOrdered(decoder)
			if err != nil {
				return nil, err
			}
			object = append(object, orderedMember{key: key.(string), value: value})
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		list := []any{}
		for decoder.More() {
			value, err := decodeOrdered(decoder)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		_, err := decoder.Token()
		return list, err
	default:
		return token, nil
	}
}

func reorderSchema(value any) any {
	object, ok := value.(orderedObject)
	if !ok {
		return value
	}
	ordered := slices.Clone(object)
	for index, member := range ordered {
		switch {
		case slices.Contains(schemaMaps, member.key):
			if named, ok := member.value.(orderedObject); ok {
				named = slices.Clone(named)
				for position := range named {
					named[position].value = reorderSchema(named[position].value)
				}
				ordered[index].value = named
			}
		case slices.Contains(schemaLists, member.key):
			if list, ok := member.value.([]any); ok {
				list = slices.Clone(list)
				for position := range list {
					list[position] = reorderSchema(list[position])
				}
				ordered[index].value = list
			}
		case slices.Contains(schemaNodes, member.key):
			ordered[index].value = reorderSchema(member.value)
		}
	}
	slices.SortStableFunc(ordered, func(left, right orderedMember) int {
		return schemaKeyRank(left.key) - schemaKeyRank(right.key)
	})
	return ordered
}

func schemaKeyRank(key string) int {
	if rank := slices.Index(schemaKeyOrder, key); rank >= 0 {
		return rank
	}
	return len(schemaKeyOrder)
}
