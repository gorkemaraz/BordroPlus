// Package api embeds the OpenAPI description of the HTTP API.
package api

import _ "embed"

// OpenAPI is openapi.yaml, served at /api/v1/openapi.yaml. A test in
// internal/server keeps it in step with the router.
//
//go:embed openapi.yaml
var OpenAPI []byte
