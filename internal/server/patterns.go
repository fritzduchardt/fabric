package restapi

import (
	"maps"
	"net/http"

	"github.com/danielmiessler/fabric/internal/plugins/db/fsdb"
	"github.com/gin-gonic/gin"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PatternsHandler defines the handler for patterns-related operations
type PatternsHandler struct {
	*StorageHandler[fsdb.Pattern]
	patterns *fsdb.PatternsEntity
}

// NewPatternsHandler creates a new PatternsHandler
func NewPatternsHandler(r *gin.Engine, patterns *fsdb.PatternsEntity) (ret *PatternsHandler) {
	// Create a storage handler but don't register any routes yet
	storageHandler := &StorageHandler[fsdb.Pattern]{storage: patterns}
	ret = &PatternsHandler{StorageHandler: storageHandler, patterns: patterns}

	// Register routes manually - use custom Get for patterns, others from StorageHandler
	r.GET("/patterns/:name", ret.Get)                       // Custom method with variables support
	r.GET("/patterns/names", ret.GetNames)                  // From StorageHandler
	r.DELETE("/patterns/:name", ret.Delete)                 // From StorageHandler
	r.GET("/patterns/exists/:name", ret.Exists)             // From StorageHandler
	r.PUT("/patterns/rename/:oldName/:newName", ret.Rename) // From StorageHandler
	r.POST("/patterns/:name", ret.Save)                     // From StorageHandler
	// Add POST route for patterns with variables in request body
	r.POST("/patterns/:name/apply", ret.ApplyPattern)
	// custom route to regenerate all patterns via Go templates
	r.POST("/patterns/generate", ret.GeneratePatterns)
	return
}

// GeneratePatterns handles POST /patterns/generate
// It reads each stored pattern template, processes it with Go's text/template
// (without any external data), and saves the rendered result back to storage.
// Pattern files are traversed recursively, preserving directory structure, skipping hidden folders.
// It also empties the output directory before generating new patterns.
func (h *PatternsHandler) GeneratePatterns(c *gin.Context) {
	fabricHome := os.Getenv("FABRIC_CONFIG_HOME")
	if fabricHome == "" {
		log.Printf("FABRIC_CONFIG_HOME not set, skipping pattern generation")
	} else {
		// generate patterns via gomplate
		patternsDir := os.Getenv("FABRIC_PATTERN_PATH")
		if patternsDir != "" {
			templatesDir := filepath.Join(patternsDir, "templates")
			outDir := filepath.Join(fabricHome, "patterns")

			// empty pattern dir before generating patterns
			if err := os.RemoveAll(outDir); err != nil {
				log.Printf("Error emptying patterns output directory %s: %v", outDir, err)
			}
			if err := os.MkdirAll(outDir, 0755); err != nil {
				log.Printf("Error creating patterns output directory %s: %v", outDir, err)
			} else {
				// ensure templatesDir exists
				if stat, err := os.Stat(templatesDir); err != nil || !stat.IsDir() {
					log.Printf("Templates directory %s does not exist or is not a directory, skipping", templatesDir)
				} else {
					// recursively walk through patternsDir, skipping hidden folders
					err := filepath.Walk(patternsDir, func(path string, fi os.FileInfo, err error) error {
						if err != nil {
							log.Printf("Error accessing path %s: %v", path, err)
							return nil
						}
						name := fi.Name()
						// skip hidden files and directories
						if strings.HasPrefix(name, ".") {
							if fi.IsDir() {
								return filepath.SkipDir
							}
							return nil
						}
						// skip the templates directory itself and its contents
						if fi.IsDir() && path == templatesDir {
							return filepath.SkipDir
						}
						// skip directories (we only process files)
						if fi.IsDir() {
							return nil
						}
						// compute relative path to mirror directory structure
						relPath, err := filepath.Rel(patternsDir, path)
						if err != nil {
							log.Printf("Error computing relative path for %s: %v", path, err)
							return nil
						}
						inputFile := path
						outputFile := filepath.Join(outDir, relPath)
						// ensure output subdirectory exists
						if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
							log.Printf("Error creating output subdirectory for %s: %v", outputFile, err)
							return nil
						}
						// invoke gomplate for each file
						cmd := exec.Command("gomplate", "-f", inputFile, "-t", templatesDir, "-o", outputFile)
						cmd.Env = os.Environ()
						if out, err := cmd.CombinedOutput(); err != nil {
							log.Printf("Error executing gomplate on %s: %v, output: %s", inputFile, err, string(out))
						} else {
							log.Printf("Generated pattern file: %s", outputFile)
						}
						return nil
					})
					if err != nil {
						log.Printf("Error walking through patterns directory %s: %v", patternsDir, err)
					}
				}
			}
		} else {
			log.Printf("FABRIC_PATTERN_PATH not set, skipping pattern generation")
		}
	}
}

// Get handles the GET /patterns/:name route - returns raw pattern without variable processing
// @Summary Get a pattern
// @Description Retrieve a pattern by name
// @Tags patterns
// @Accept json
// @Produce json
// @Param name path string true "Pattern name"
// @Success 200 {object} fsdb.Pattern
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Router /patterns/{name} [get]
func (h *PatternsHandler) Get(c *gin.Context) {
	name := c.Param("name")

	// Get the raw pattern content without any variable processing
	content, err := h.patterns.Load(name + "/" + h.patterns.SystemPatternFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	// Return raw pattern in the same format as the processed patterns
	pattern := &fsdb.Pattern{
		Name:        name,
		Description: "",
		Pattern:     string(content),
	}
	c.JSON(http.StatusOK, pattern)
}

// PatternApplyRequest represents the request body for applying a pattern
type PatternApplyRequest struct {
	Input     string            `json:"input"`
	Variables map[string]string `json:"variables,omitempty"`
}

// ApplyPattern handles the POST /patterns/:name/apply route
// @Summary Apply pattern with variables
// @Description Apply a pattern with variable substitution
// @Tags patterns
// @Accept json
// @Produce json
// @Param name path string true "Pattern name"
// @Param request body PatternApplyRequest true "Pattern application request"
// @Success 200 {object} fsdb.Pattern
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Router /patterns/{name}/apply [post]
func (h *PatternsHandler) ApplyPattern(c *gin.Context) {
	name := c.Param("name")

	var request PatternApplyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Merge query parameters with request body variables (body takes precedence)
	variables := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			variables[key] = values[0]
		}
	}
	maps.Copy(variables, request.Variables)

	pattern, err := h.patterns.GetApplyVariables(name, variables, request.Input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, pattern)
}
