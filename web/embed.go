package web

import "embed"

//go:embed templates/*.html
var Templates embed.FS

//go:embed static/*
var Static embed.FS

//go:embed openapi.yaml
var OpenAPI []byte
