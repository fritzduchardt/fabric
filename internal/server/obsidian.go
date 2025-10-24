package restapi

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielmiessler/fabric/internal/util"
	"github.com/gin-gonic/gin"
)

// ObsidianHandler handles listing, fetching, and deleting Obsidian markdown files
type ObsidianHandler struct {
	vaultPaths map[string]string
}

// NewObsidianHandler registers endpoints to list, retrieve, and delete Obsidian files
func NewObsidianHandler(r *gin.Engine) {
	vaultPaths := make(map[string]string)
	basePath := os.Getenv("OBSIDIAN_BASE_PATH")
	if basePath == "" {
		log.Printf("OBSIDIAN_BASE_PATH environment variable not set")
	}
	// Check for numbered vault paths (OBSIDIAN_VAULT_PATH_1, OBSIDIAN_VAULT_PATH_2, etc.)
	for i := 1; i <= 10; i++ {
		envKey := fmt.Sprintf("OBSIDIAN_VAULT_PATH_%d", i)
		vaultPath := os.Getenv(envKey)
		if vaultPath != "" {
			// Extract folder name from path
			parts := strings.Split(vaultPath, "/")
			folderName := parts[len(parts)-2]
			vaultPaths[folderName] = basePath + "/" + vaultPath
			log.Printf("Found Obsidian vault: %s at %s", folderName, vaultPath)
		}
	}
	handler := &ObsidianHandler{
		vaultPaths: vaultPaths,
	}

	// List all markdown files under all vaults
	r.GET("/obsidian/files", handler.List)
	// Get the content of a specific markdown file by relative name or path (supports subpaths)
	r.GET("/obsidian/file/*name", handler.Get)
	// Delete a specific markdown file
	r.DELETE("/obsidian/file/*name", handler.Delete)
}

// List returns a JSON array of all .md files in all vaults (relative paths)
func (h *ObsidianHandler) List(c *gin.Context) {
	var files []string

	for prefix, rootPath := range h.vaultPaths {
		err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			name := info.Name()
			if strings.HasPrefix(name, ".") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !info.IsDir() && filepath.Ext(name) == ".md" {
				rel, err := filepath.Rel(rootPath, path)
				if err != nil {
					return err
				}
				// Prefix all paths with their vault name
				files = append(files, filepath.Join(prefix, rel))
			}
			return nil
		})
		if err != nil {
			log.Printf("Error walking vault %s: %v", prefix, err)
		}
	}

	c.JSON(http.StatusOK, files)
}

// Get reads and returns the content of the requested .md file as text/markdown
// Supports files in all configured vaults. Files are referenced by prefixing
// the path with the vault name, e.g. "private/note.md" or "shared/note.md"
func (h *ObsidianHandler) Get(c *gin.Context) {
	name := c.Param("name")
	filePath := util.ObsidianPath(name)

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	header := fmt.Sprintf("FILENAME: %s\n\n", strings.TrimPrefix(name, "/"))
	content := append([]byte(header), data...)
	c.Data(http.StatusOK, "text/markdown", content)
}

// Delete deletes the specified .md file from the configured vault
func (h *ObsidianHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	filePath := util.ObsidianPath(name)
	err := os.Remove(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		} else if os.IsPermission(err) {
			c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": fmt.Sprintf("deleted %s", filePath)})
}
