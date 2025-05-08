package restapi

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// ObsidianHandler handles listing and fetching Obsidian markdown files
type ObsidianHandler struct {
	vaultPath string
}

// NewObsidianHandler registers endpoints to list and retrieve Obsidian files
func NewObsidianHandler(r *gin.Engine) {
	vaultPath := os.Getenv("OBSIDIAN_VAULT_PATH")
	if vaultPath == "" {
		vaultPath = filepath.Join(os.Getenv("HOME"), "Documents/Obsidian")
		log.Printf("Obsidian Vault Path not set. Defaulting to: %s", vaultPath)
	}
	handler := &ObsidianHandler{vaultPath: vaultPath}
	// List all markdown files under the vault
	r.GET("/obsidian/files", handler.List)
	// Get the content of a specific markdown file by relative name
	r.GET("/obsidian/file/:name", handler.Get)
}

// List returns a JSON array of all .md files in the vault (relative paths)
func (h *ObsidianHandler) List(c *gin.Context) {
	var files []string
	err := filepath.Walk(h.vaultPath, func(path string, info os.FileInfo, err error) error {
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
			rel, err := filepath.Rel(h.vaultPath, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, files)
}

// Get reads and returns the content of the requested .md file as text/markdown
func (h *ObsidianHandler) Get(c *gin.Context) {
	name := c.Param("name")
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	clean := filepath.Clean(name)
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
	}
	filePath := filepath.Join(h.vaultPath, clean)
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.Data(http.StatusOK, "text/markdown", data)
}
