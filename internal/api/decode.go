package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// decodeObject parses a single resource from JSON or YAML bytes.
func decodeObject(b []byte) (v1alpha1.Object, error) {
	var obj v1alpha1.Object
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return obj, fmt.Errorf("empty body")
	}
	if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return obj, fmt.Errorf("invalid JSON: %w", err)
		}
		return obj, nil
	}
	if err := yaml.Unmarshal(trimmed, &obj); err != nil {
		return obj, fmt.Errorf("invalid YAML: %w", err)
	}
	return obj, nil
}

// decodeMultiDoc parses a multi-document YAML stream (--- separated). Empty
// documents are skipped.
func decodeMultiDoc(r io.Reader) ([]v1alpha1.Object, error) {
	dec := yaml.NewDecoder(io.LimitReader(r, 8<<20))
	var objs []v1alpha1.Object
	for {
		var obj v1alpha1.Object
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid YAML document: %w", err)
		}
		if obj.Kind == "" && obj.Metadata.Name == "" {
			continue // empty doc
		}
		objs = append(objs, obj)
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no resources found in body")
	}
	return objs, nil
}

func decodeJSON(r io.Reader, v interface{}) error {
	dec := json.NewDecoder(io.LimitReader(r, 1<<20))
	if err := dec.Decode(v); err != nil && err != io.EOF {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func decodeJSONBytes(b []byte, v interface{}) error {
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
