// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

// Format is a structured output format, matching the -o/--output values that
// select one.
type Format string

const (
	FormatJSON Format = "json"
	FormatYAML Format = "yaml"
)

// Encode writes the report in one of the structured formats. YAML goes through
// the JSON tags, so both formats carry the same field names and the same
// absent fields.
func (r *Report) Encode(w io.Writer, format Format) error {
	switch format {
	case FormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		// Probe failure messages are shell and HTTP text, and escaping them
		// into & would make the output harder to read than it is worth.
		enc.SetEscapeHTML(false)
		return enc.Encode(r)
	case FormatYAML:
		b, err := yaml.Marshal(r)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	default:
		return fmt.Errorf("unknown output format %q: must be one of %s, %s", format, FormatJSON, FormatYAML)
	}
}
