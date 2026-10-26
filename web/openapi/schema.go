// SPDX-License-Identifier: Apache-2.0

package openapi

import (
	"bytes"
	"encoding/json"
)

// object is a JSON object whose keys keep their order.
type object struct {
	keys []string
	vals []any
}

func (o *object) add(k string, v any) {
	o.keys = append(o.keys, k)
	o.vals = append(o.vals, v)
}

func (o object) MarshalJSON() ([]byte, error) { return o.marshal() }

func (o object) marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshal(o.vals[i])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshal is json.Marshal without HTML escaping, so descriptions keep
// their < and &.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
