// Command openapi converts Jotist's generated Swagger document to OpenAPI 3.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: openapi swagger.json")
	}
	if err := convert(os.Args[1], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func convert(path string, output io.Writer) error {
	input, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var source openapi2.T
	if err := json.Unmarshal(input, &source); err != nil {
		return fmt.Errorf("decode Swagger: %w", err)
	}
	spec, err := openapi2conv.ToV3(&source)
	if err != nil {
		return fmt.Errorf("convert to OpenAPI 3: %w", err)
	}
	for _, item := range spec.Paths.Map() {
		for _, operation := range item.Operations() {
			if operation.RequestBody != nil && operation.RequestBody.Value != nil {
				describeUploads(operation.RequestBody.Value.Content)
			}
		}
	}
	if err := spec.Validate(context.Background()); err != nil {
		return fmt.Errorf("validate OpenAPI 3: %w", err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(spec)
}

// Swagger 2 can describe a single multipart file. OpenAPI 3 also describes
// repeatable files and raw binary bodies as native file inputs.
func describeUploads(content openapi3.Content) {
	if raw := content["application/octet-stream"]; raw != nil {
		raw.Schema = &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithFormat("binary")}
	}
	multipart := content["multipart/form-data"]
	if multipart == nil || multipart.Schema == nil || multipart.Schema.Value == nil {
		return
	}
	for name, property := range multipart.Schema.Value.Properties {
		if property.Value == nil || property.Value.Format != "binary" || property.Value.Extensions["x-repeatable"] != true {
			continue
		}
		description := property.Value.Description
		delete(property.Value.Extensions, "x-repeatable")
		files := openapi3.NewArraySchema().WithItems(property.Value).WithMinItems(1)
		files.Description = description
		multipart.Schema.Value.Properties[name] = &openapi3.SchemaRef{Value: files}
	}
}
