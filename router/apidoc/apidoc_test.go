package apidoc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/router"
	"github.com/gin-gonic/gin"
	"github.com/go-openapi/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swaggo/swag"
)

var update = flag.Bool("update", false, "rewrite docs/openapi from the annotations")

// searchDirs hold the annotations, relative to the repository root. The
// first holds the general information of each document.
var searchDirs = []string{
	"router/apidoc",
	"router",
	"controller",
	"internal/module/console/transport/http",
	"internal/module/currency/transport/http",
	"internal/module/mail/transport/http",
	"internal/module/payment/transport/http",
}

// typeOverrides name the Swagger type of library types whose JSON form swag
// cannot read from their fields.
var typeOverrides = map[string]string{
	"encoding/json.Number": "number",
	// Raw JSON, mostly objects; Swagger 2.0 has no "any value" type.
	"encoding/json.RawMessage":                       "object",
	"github.com/QuantumNous/new-api/model.JSONValue": "object",
	"github.com/shopspring/decimal.Decimal":          "string",
	// A soft-deleted row's time, or null.
	"gorm.io/gorm.DeletedAt": "string",
}

var (
	ginPathParam     = regexp.MustCompile(`[:*]([A-Za-z0-9_]+)`)
	swaggerPathParam = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
)

// TestOpenAPIDocuments generates docs/openapi/api.json, holding the operations
// tagged with api.go's tags, and relay.json, holding those tagged with
// relay.go's, and checks them against the files and the registered routes.
// Run `make openapi` (this test with -update) to rewrite the files.
func TestOpenAPIDocuments(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	relayTags := declaredTags(t, "relay.go")
	apiFilter := make([]string, len(relayTags))
	for i, tag := range relayTags {
		apiFilter[i] = "!" + tag
	}
	documents := []struct {
		name        string // under docs/openapi
		generalInfo string
		swagger     *spec.Swagger
	}{
		{"api.json", "api.go", generateDocument(t, root, "api.go", strings.Join(apiFilter, ","))},
		{"relay.json", "relay.go", generateDocument(t, root, "relay.go", strings.Join(relayTags, ","))},
	}

	t.Run("files are up to date", func(t *testing.T) {
		for _, document := range documents {
			raw, err := common.Marshal(document.swagger)
			require.NoError(t, err)
			var content bytes.Buffer
			require.NoError(t, json.Indent(&content, raw, "", "    "))
			content.WriteByte('\n')
			path := filepath.Join(root, "docs", "openapi", document.name)
			if *update {
				require.NoError(t, os.WriteFile(path, content.Bytes(), 0o644))
				continue
			}
			committed, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(committed, content.Bytes()), "docs/openapi/%s is out of date; run make openapi", document.name)
		}
	})

	t.Run("operations cover the registered routes", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		router.SetApiRouter(engine)
		router.SetDashboardRouter(engine)
		router.SetRelayRouter(engine)
		router.SetTaskPluginProtocolRouter(engine)
		router.SetVideoRouter(engine)
		router.SetTaskRouter(engine)
		registered := map[string]bool{}
		for _, route := range engine.Routes() {
			registered[route.Method+" "+ginPathParam.ReplaceAllString(route.Path, "{$1}")] = true
		}

		var problems []string
		documented := map[string]bool{}
		for _, document := range documents {
			name := document.name
			tags := declaredTags(t, document.generalInfo)
			usedTags := map[string]bool{}
			for path, item := range document.swagger.Paths.Paths {
				operations := map[string]*spec.Operation{
					http.MethodGet: item.Get, http.MethodPut: item.Put, http.MethodPost: item.Post, http.MethodDelete: item.Delete,
					http.MethodOptions: item.Options, http.MethodHead: item.Head, http.MethodPatch: item.Patch,
				}
				for method, operation := range operations {
					if operation == nil {
						continue
					}
					key := method + " " + path
					documented[key] = true
					if strings.HasPrefix(path, "/api/") != (name == "api.json") {
						problems = append(problems, key+" is in "+name)
					}
					if operation.Summary == "" {
						problems = append(problems, key+" has no @Summary")
					}
					if len(operation.Tags) != 1 || !slices.Contains(tags, operation.Tags[0]) {
						problems = append(problems, key+" needs one @Tags declared in "+name+"'s general information")
					}
					for _, tag := range operation.Tags {
						usedTags[tag] = true
					}
					var pathParams []string
					for _, parameter := range operation.Parameters {
						if parameter.In == "path" {
							pathParams = append(pathParams, parameter.Name)
						}
					}
					for _, match := range swaggerPathParam.FindAllStringSubmatch(path, -1) {
						if !slices.Contains(pathParams, match[1]) {
							problems = append(problems, key+" needs a path @Param "+match[1])
						}
					}
					for _, parameter := range pathParams {
						if !strings.Contains(path, "{"+parameter+"}") {
							problems = append(problems, key+" declares path @Param "+parameter+" that is not in the path")
						}
					}
					for _, requirement := range operation.Security {
						for scheme := range requirement {
							if _, ok := document.swagger.SecurityDefinitions[scheme]; !ok {
								problems = append(problems, key+" uses @Security "+scheme+", which "+name+" does not define")
							}
						}
					}
				}
			}
			for _, tag := range tags {
				if !usedTags[tag] {
					problems = append(problems, name+" declares tag "+tag+" that no operation uses")
				}
			}
			// swag names a type after its import path when two packages of
			// the same name define it.
			for definition := range document.swagger.Definitions {
				if strings.HasPrefix(definition, "github_com_") {
					problems = append(problems, name+" has clashing type names: rename "+definition)
				}
			}
		}
		for key := range registered {
			if !documented[key] {
				problems = append(problems, key+" is registered but not documented")
			}
		}
		for key := range documented {
			if !registered[key] {
				problems = append(problems, key+" is documented but not registered")
			}
		}
		slices.Sort(problems)
		assert.Empty(t, strings.Join(problems, "\n"))
	})
}

func generateDocument(t *testing.T, root, generalInfo, tags string) *spec.Swagger {
	t.Helper()
	parser := swag.New(
		swag.SetParseDependency(1),
		swag.ParseUsingGoList(true),
		swag.SetPackagePrefix("github.com/QuantumNous/new-api"),
		swag.SetOverrides(typeOverrides),
		swag.SetTags(tags),
		swag.SetStrict(true),
		swag.SetDebugger(log.New(io.Discard, "", 0)),
	)
	parser.ParseInternal = true
	parser.ParseFuncBody = true
	// encoding/json names a field without a json tag after the field itself.
	parser.PropNamingStrategy = swag.PascalCase
	dirs := make([]string, len(searchDirs))
	for i, dir := range searchDirs {
		dirs[i] = filepath.Join(root, dir)
	}
	require.NoError(t, parser.ParseAPIMultiSearchDir(dirs, generalInfo, 100))
	return parser.GetSwagger()
}

// declaredTags lists the @tag.name lines of a general information file.
func declaredTags(t *testing.T, generalInfo string) []string {
	t.Helper()
	file, err := os.Open(generalInfo)
	require.NoError(t, err)
	defer file.Close()
	var tags []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if _, tag, ok := strings.Cut(scanner.Text(), "@tag.name"); ok {
			tags = append(tags, strings.TrimSpace(tag))
		}
	}
	require.NoError(t, scanner.Err())
	return tags
}
