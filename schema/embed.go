// SPDX-License-Identifier: Apache-2.0
package schema

import _ "embed"

// OpenAPIJSON holds the embedded OpenAPI 3.1 wire contract.
//
//go:embed openapi.json
var OpenAPIJSON []byte
