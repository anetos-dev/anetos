// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// SchemaOptions adapts a [Schema] to a provider's dialect of JSON Schema
// ([Schema.Map]): what drivers use to send tool inputs and structured
// outputs.
type SchemaOptions struct {
	// Keywords are the constraint keywords the provider accepts, of
	// [ConstraintKeywords]. The others are written into the description
	// in words ("(at most 20 characters)"), for the model to follow; the
	// answer is validated either way.
	Keywords []string
	// AllRequired lists every property as required, and makes the
	// optional ones nullable (OpenAI's strict mode).
	AllRequired bool
	// NullableAnyOf writes a nullable schema as anyOf [the schema,
	// {"type": "null"}], instead of a type array.
	NullableAnyOf bool
	// Formats, if not empty, are the string formats the provider takes,
	// when Keywords has "format"; others go into the description.
	Formats []string
}

// ConstraintKeywords are the constraint keywords a [Schema] can have.
var ConstraintKeywords = []string{"format", "minLength", "maxLength", "minimum", "maximum", "minItems", "maxItems", "minProperties", "maxProperties"}

// Map returns the schema as a JSON Schema object adapted by o, for a
// provider's SDK. Objects' "properties" keep their order when marshaled
// to JSON (the value isn't a map).
func (s *Schema) Map(o SchemaOptions) map[string]any { return s.mapOf(o, s.Nullable) }

func (s *Schema) mapOf(o SchemaOptions, nullable bool) map[string]any {
	m := map[string]any{}
	if s.Type != "" {
		if nullable && !o.NullableAnyOf {
			m["type"] = []string{s.Type, "null"}
		} else {
			m["type"] = s.Type
		}
	}
	var extras []string
	if s.Type == "object" {
		props := make(orderedProperties, len(s.Properties))
		required := slices.Clone(s.Required)
		for i, p := range s.Properties {
			pn := p.Schema.Nullable
			if o.AllRequired && !slices.Contains(s.Required, p.Name) {
				pn = true
				required = append(required, p.Name)
			}
			props[i] = property{p.Name, p.Schema.mapOf(o, pn)}
		}
		if o.AllRequired {
			order := make(map[string]int, len(s.Properties))
			for i, p := range s.Properties {
				order[p.Name] = i
			}
			slices.SortFunc(required, func(a, b string) int { return order[a] - order[b] })
		}
		m["properties"] = props
		if len(required) > 0 {
			m["required"] = required
		}
		if s.AdditionalProperties != nil {
			m["additionalProperties"] = s.AdditionalProperties.mapOf(o, s.AdditionalProperties.Nullable)
		} else {
			m["additionalProperties"] = false
		}
	}
	if s.Items != nil {
		m["items"] = s.Items.mapOf(o, s.Items.Nullable)
	}
	if len(s.Enum) > 0 {
		enum := slices.Clone(s.Enum)
		hasNull := slices.Contains(enum, nil)
		switch {
		case nullable && o.NullableAnyOf:
			enum = slices.DeleteFunc(enum, func(v any) bool { return v == nil }) // null is the anyOf's other branch
		case nullable && !hasNull:
			enum = append(enum, nil)
		}
		m["enum"] = enum
	}
	keep := func(k string, v any) {
		if slices.Contains(o.Keywords, k) {
			m[k] = v
		} else {
			extras = append(extras, constraintText(k, v))
		}
	}
	if s.Format != "" {
		if len(o.Formats) > 0 && !slices.Contains(o.Formats, s.Format) {
			extras = append(extras, constraintText("format", s.Format))
		} else {
			keep("format", s.Format)
		}
	}
	for _, c := range []struct {
		k string
		v *int
	}{{"minLength", s.MinLength}, {"maxLength", s.MaxLength}, {"minItems", s.MinItems}, {"maxItems", s.MaxItems},
		{"minProperties", s.MinProperties}, {"maxProperties", s.MaxProperties}} {
		if c.v != nil {
			keep(c.k, *c.v)
		}
	}
	if s.Minimum != nil {
		keep("minimum", *s.Minimum)
	}
	if s.Maximum != nil {
		keep("maximum", *s.Maximum)
	}
	desc := s.Description
	switch {
	case len(extras) > 0 && desc != "":
		desc += " (" + strings.Join(extras, "; ") + ")"
	case len(extras) > 0:
		desc = strings.Join(extras, "; ")
	}
	if desc != "" {
		m["description"] = desc
	}
	if nullable && o.NullableAnyOf && s.Type != "" {
		return map[string]any{"anyOf": []any{m, map[string]any{"type": "null"}}}
	}
	return m
}

// constraintText says a constraint keyword's value in words, for a
// description: "at most 20 characters".
func constraintText(k string, v any) string {
	n := fmt.Sprint(v)
	plural := func(word string) string {
		if n == "1" {
			return n + " " + word
		}
		return n + " " + word + "s"
	}
	switch k {
	case "format":
		return "format: " + n
	case "minLength":
		return "at least " + plural("character")
	case "maxLength":
		return "at most " + plural("character")
	case "minimum":
		return "at least " + n
	case "maximum":
		return "at most " + n
	case "minItems":
		return "at least " + plural("item")
	case "maxItems":
		return "at most " + plural("item")
	case "minProperties":
		return "at least " + plural("key")
	case "maxProperties":
		return "at most " + plural("key")
	}
	return k + ": " + n
}

// orderedProperties are an object schema's properties, marshaled in
// order.
type orderedProperties []property

type property struct {
	name   string
	schema map[string]any
}

// MarshalJSON writes the properties as a JSON object, in order.
func (p orderedProperties) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, pr := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(pr.name))
		b.WriteByte(':')
		data, err := json.Marshal(pr.schema)
		if err != nil {
			return nil, fmt.Errorf("property %s: %w", pr.name, err)
		}
		b.Write(data)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// jsonString is s as a JSON string.
func jsonString(s string) []byte {
	data, _ := json.Marshal(s) // a string always marshals
	return data
}
