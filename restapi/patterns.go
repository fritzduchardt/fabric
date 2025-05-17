package restapi

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danielmiessler/fabric/plugins/db/fsdb"
	"github.com/gin-gonic/gin"
)

// PatternsHandler defines the handler for patterns-related operations
type PatternsHandler struct {
	*StorageHandler[fsdb.Pattern]
	patterns *fsdb.PatternsEntity
}

// NewPatternsHandler creates a new PatternsHandler
func NewPatternsHandler(r *gin.Engine, patterns *fsdb.PatternsEntity) (ret *PatternsHandler) {
	ret = &PatternsHandler{
		StorageHandler: NewStorageHandler(r, "patterns", patterns),
		patterns:       patterns,
	}

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
	c.JSON(http.StatusOK, gin.H{"message": "Patterns regenerated successfully"})
}
