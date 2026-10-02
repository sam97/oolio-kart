// Package api holds the OpenAPI document of the food ordering API.
package api

import _ "embed"

// OpenAPI is the API's OpenAPI 3.1 document.
//
//go:embed openapi.yaml
var OpenAPI []byte
