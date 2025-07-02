package restapi

import (
	"fmt"
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
	vaultPath       string
	sharedVaultPath string
}

// NewObsidianHandler registers endpoints to list and retrieve Obsidian files
func NewObsidianHandler(r *gin.Engine) {
	vaultPath := os.Getenv("OBSIDIAN_VAULT_PATH")
	if vaultPath == "" {
		vaultPath = filepath.Join(os.Getenv("HOME"), "Documents/Obsidian")
		log.Printf("Obsidian Vault Path not set. Defaulting to: %s", vaultPath)
	}
	sharedVaultPath := os.Getenv("OBSIDIAN_VAULT_PATH_SHARED")
	if sharedVaultPath == "" {
		sharedVaultPath = filepath.Join(vaultPath, "shared")
		log.Printf("Obsidian Shared Vault Path not set. Defaulting to: %s", sharedVaultPath)
	}
	handler := &ObsidianHandler{
		vaultPath:       vaultPath,
		sharedVaultPath: sharedVaultPath,
	}
	// List all markdown files under the vault and shared vault
	r.GET("/obsidian/files", handler.List)
	// Get the content of a specific markdown file by relative name or path (supports subpaths)
	r.GET("/obsidian/file/*name", handler.Get)
}

// List returns a JSON array of all .md files in the vault and shared vault (relative paths)
func (h *ObsidianHandler) List(c *gin.Context) {
	var files []string
	roots := []struct {
		root   string
		prefix string
	}{
		{h.vaultPath, ""},
		{h.sharedVaultPath, "shared"},
	}
	for _, entry := range roots {
		err := filepath.Walk(entry.root, func(path string, info os.FileInfo, err error) error {
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
				rel, err := filepath.Rel(entry.root, path)
				if err != nil {
					return err
				}
				if entry.prefix != "" {
					rel = filepath.Join(entry.prefix, rel)
				}
				files = append(files, rel)
			}
			return nil
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, files)
}

// Get reads and returns the content of the requested .md file as text/markdown
// Supports files in primary vault and shared vault. Shared files may be referenced
// by prefixing the path with "shared/". Prepends the clean filepath as the first line prefixed by FILENAME:
func (h *ObsidianHandler) Get(c *gin.Context) {
	name := c.Param("name")
	name = strings.TrimPrefix(name, "/")
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	clean := filepath.Clean(name)
	parts := strings.Split(clean, string(os.PathSeparator))
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
	}
	var filePath string
	var data []byte
	var err error

	// Determine if path is explicitly shared
	if strings.HasPrefix(clean, "shared"+string(os.PathSeparator)) {
		rest := strings.TrimPrefix(clean, "shared"+string(os.PathSeparator))
		filePath = filepath.Join(h.sharedVaultPath, rest)
		data, err = ioutil.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
	} else {
		// Try primary vault first
		filePath = filepath.Join(h.vaultPath, clean)
		data, err = ioutil.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				// Fallback to shared vault
				filePath = filepath.Join(h.sharedVaultPath, clean)
				data, err = ioutil.ReadFile(filePath)
				if err != nil {
					if os.IsNotExist(err) {
						c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
					} else {
						c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					}
					return
				}
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	}

	header := fmt.Sprintf("FILENAME: %s\n\n", filePath)
	content := append([]byte(header), data...)
	c.Data(http.StatusOK, "text/markdown", content)
}
