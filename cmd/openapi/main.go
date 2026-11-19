// Command openapi exports Jotist's OpenAPI contract for the project site and
// clients that import the specification from a release.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/api"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: openapi OUTPUT.json")
	}
	gin.SetMode(gin.ReleaseMode)
	spec, err := api.OpenAPISpec()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[1], spec, 0o644); err != nil {
		log.Fatal(err)
	}
}
