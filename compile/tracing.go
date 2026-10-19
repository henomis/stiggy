// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package compile

import (
	"github.com/henomis/stiggy/spec"
)

func (c *compiler) tracing() {
	t := c.fleet.Tracing
	if t == nil {
		return
	}

	const path = "tracing"

	switch t.Exporter {
	case spec.TracingOTLP, spec.TracingJSONFile, spec.TracingText:
	case "":
		c.errorf(path+".exporter", "required: %s, %s or %s", spec.TracingOTLP, spec.TracingJSONFile, spec.TracingText)
	default:
		c.errorf(path+".exporter", "unknown exporter %q: want %s, %s or %s", t.Exporter,
			spec.TracingOTLP, spec.TracingJSONFile, spec.TracingText)
	}

	if t.Exporter == spec.TracingJSONFile && t.File == "" {
		c.errorf(path+".file", "required by the jsonfile exporter")
	}

	if t.Exporter != spec.TracingJSONFile && t.File != "" {
		c.errorf(path+".file", "only for the jsonfile exporter")
	}

	if t.Exporter != spec.TracingOTLP {
		for _, f := range []struct {
			key string
			set bool
		}{
			{"endpoint", t.Endpoint != ""},
			{"insecure", t.Insecure},
			{"headers", len(t.Headers) > 0},
			{"service_name", t.ServiceName != ""},
		} {
			if f.set {
				c.errorf(path+"."+f.key, "only for the otlp exporter")
			}
		}
	}

	if t.Content != nil && t.Content.MaxBytes < 0 {
		c.errorf(path+".content.max_bytes", "must not be negative")
	}
}
