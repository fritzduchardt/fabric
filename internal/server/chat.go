package restapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/danielmiessler/fabric/internal/chat"
	"github.com/danielmiessler/fabric/internal/core"
	"github.com/danielmiessler/fabric/internal/domain"
	"github.com/danielmiessler/fabric/internal/plugins/db/fsdb"
	"github.com/danielmiessler/fabric/internal/util"
	"github.com/gin-gonic/gin"
)

var (
	urlRegex  = regexp.MustCompile(`https?://[^\s]+`)
	dateRegex = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
)

type ChatHandler struct {
	registry *core.PluginRegistry
	db       *fsdb.Db
}

type PromptRequest struct {
	UserInput    string            `json:"userInput"`
	Vendor       string            `json:"vendor"`
	Model        string            `json:"model"`
	ContextName  string            `json:"contextName"`
	PatternName  string            `json:"patternName"`
	StrategyName string            `json:"strategyName"`        // Optional strategy name
	Variables    map[string]string `json:"variables,omitempty"` // Pattern variables
	ObsidianFile string            `json:"obsidianFile"`
	SessionName  string            `json:"sessionName"`
}

type ChatRequest struct {
	Prompts            []PromptRequest `json:"prompts"`
	Language           string          `json:"language"` // Add Language field to bind from request
	domain.ChatOptions                 // Embed the ChatOptions from common package
}

type StreamResponse struct {
	Type    string `json:"type"`    // "content", "error", "complete"
	Format  string `json:"format"`  // "markdown", "mermaid", "plain"
	Content string `json:"content"` // The actual content
}

func NewChatHandler(r *gin.Engine, registry *core.PluginRegistry, db *fsdb.Db) *ChatHandler {
	handler := &ChatHandler{
		registry: registry,
		db:       db,
	}

	r.POST("/chat", handler.HandleChat)
	r.POST("/store", handler.StoreMessage)
	r.DELETE("/deletepattern/:name", handler.DeletePattern)
	return handler
}

// DeletePattern deletes a pattern file from FABRIC_CONFIG_HOME/patterns
func (h *ChatHandler) DeletePattern(c *gin.Context) {
	log.Printf("[DEBUG] Entering DeletePattern handler")
	name := c.Param("name")
	log.Printf("[DEBUG] Pattern name to delete: %s", name)

	fabricPatternPath := os.Getenv("FABRIC_PATTERN_PATH")
	if fabricPatternPath == "" {
		log.Printf("[ERROR] FABRIC_PATTERN_PATH not set")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FABRIC_PATTERN_PATH not set"})
		return
	}
	log.Printf("[DEBUG] FABRIC_PATTERN_PATH: %s", fabricPatternPath)
	target := filepath.Join(fabricPatternPath, name)
	log.Printf("[DEBUG] Attempting to delete target: %s", target)

	err := os.RemoveAll(target)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[WARNING] Pattern not found: %s", name)
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Pattern not found: %s", name)})
			return
		}
		log.Printf("[ERROR] Error deleting pattern %s: %v", name, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error deleting pattern: %v", err)})
		return
	}
	log.Printf("Deleted pattern: %s", target)
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Pattern %s deleted successfully", name)})
	log.Printf("[DEBUG] Exiting DeletePattern handler")
}

func (h *ChatHandler) HandleChat(c *gin.Context) {
	log.Printf("[DEBUG] Entering HandleChat handler")
	var request ChatRequest

	if err := c.BindJSON(&request); err != nil {
		log.Printf("Error binding JSON: %v", err)
		c.Writer.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request format: %v", err)})
		return
	}
	log.Printf("[DEBUG] Chat request bound successfully: %+v", request)

	fabricHome := os.Getenv("FABRIC_CONFIG_HOME")
	if fabricHome == "" {
		log.Printf("FABRIC_CONFIG_HOME not set, skipping context file write")
	} else {
		log.Printf("[DEBUG] FABRIC_CONFIG_HOME is set to: %s", fabricHome)
		contextDir := filepath.Join(fabricHome, "contexts")
		if err := os.MkdirAll(contextDir, 0755); err != nil {
			log.Printf("Error creating context directory %s: %v", contextDir, err)
		} else {
			log.Printf("[DEBUG] Context directory ensured: %s", contextDir)
			var patternSet = make(map[string]bool)
			for _, p := range request.Prompts {
				if p.PatternName != "" {
					patternSet[p.PatternName] = true
				}
			}
			var patternNamesSlice []string
			for name := range patternSet {
				patternNamesSlice = append(patternNamesSlice, name)
			}
			patternNames := strings.Join(patternNamesSlice, ", ")
			log.Printf("[DEBUG] Pattern names for context: %s", patternNames)
			now := time.Now()
			personalName := os.Getenv("PERSONAL_NAME")
			if personalName == "" {
				personalName = "Fritz"
			}
			log.Printf("[DEBUG] Personal name for context: %s", personalName)
			filename := filepath.Join(contextDir, "general_context.md")
			content := fmt.Sprintf("# CONTEXT\n - User name: %s\n - The Current Date is: %s\n - Pattern name(s): %s\n - This is not a chat\n", personalName, now.Format("2006-01-02"), patternNames)
			if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
				log.Printf("Error writing context file %s: %v", filename, err)
			} else {
				log.Printf("Wrote context file: %s", filename)
			}
		}
	}

	log.Printf("Received chat request - Language: '%s', Prompts: %d", request.Language, len(request.Prompts))

	log.Printf("[DEBUG] Setting SSE headers")
	c.Writer.Header().Set("Content-Type", "text/readystream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	clientGone := c.Writer.CloseNotify()

	for i, prompt := range request.Prompts {
		select {
		case <-clientGone:
			log.Printf("Client disconnected")
			return
		default:
			log.Printf("Processing prompt %d: Model=%s Pattern=%s Context=%s ObsidianFile=%s",
				i+1, prompt.Model, prompt.PatternName, prompt.ContextName, prompt.ObsidianFile)

			streamChan := make(chan string)

			go func(p PromptRequest) {
				log.Printf("[DEBUG] Goroutine started for prompt: %+v", p)
				defer close(streamChan)

				links := urlRegex.FindAllString(p.UserInput, -1)
				log.Printf("[DEBUG] Found %d links in user input: %v", len(links), links)
				totalLinkChars := 0
				const maxLinkChars = 500000
				for _, link := range links {
					log.Printf("[DEBUG] Processing link: %s", link)
					if strings.HasSuffix(link, ".rss") || strings.HasSuffix(link, ".xml") || strings.HasSuffix(link, ".xml#") {
						log.Printf("[DEBUG] Detected RSS/XML feed: %s", link)
						md, err := util.ConvertRSSFeedToMarkdown(link)
						if totalLinkChars+len(md) > maxLinkChars {
							log.Printf("Skipping URL %s: would exceed accumulated content limit", link)
							continue
						}
						totalLinkChars += len(md)
						if err != nil {
							log.Printf("Error converting RSS feed %s: %v", link, err)
						} else {
							p.UserInput = p.UserInput + "\nRSS Feed Markdown:\n" + md
							log.Printf("Added RSS feed markdown for: %s. Total link chars: %d", link, totalLinkChars)
						}
						continue
					}
					if totalLinkChars >= maxLinkChars {
						log.Printf("Skipping remaining URLs: accumulated link content too large")
						break
					}
					resp, err := http.Get(link)
					if err != nil {
						log.Printf("Error fetching URRL %s: %v", link, err)
						continue
					}
					body, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						log.Printf("Error reading response from URL %s: %v", link, err)
						continue
					}
					text := util.StripTags(string(body))
					if totalLinkChars+len(text) > maxLinkChars {
						log.Printf("Skipping URL %s: would exceed accumulated content limit", link)
						continue
					}
					content := "URL: " + link + "\n" + text
					p.UserInput = p.UserInput + "\nURL Content:\n" + content
					totalLinkChars += len(text)
					log.Printf("Added content from URL: %s. Total link chars: %d", link, totalLinkChars)
				}

				var obsidianFilePath string
				if p.ObsidianFile != "" {
					log.Printf("[DEBUG] Processing ObsidianFile: %s", p.ObsidianFile)
					if p.ObsidianFile == "weaviate" {
						log.Printf("[DEBUG] Detected weaviate journal spec: %s", p.ObsidianFile)
						path, contentToUse, err := readWeaviateJournal(p.UserInput, p.ObsidianFile)
						if err == nil {
							fileContentAmended := "FILENAME: " + path + "\n" + contentToUse
							p.UserInput = p.UserInput + "\nJournal File:\n" + fileContentAmended
							log.Printf("[INFO] Added content from weaviate spec: %s (path: %s)", p.ObsidianFile, path)
						} else {
							log.Printf("[ERROR] Error fetching weaviate journal for %s: %v", p.ObsidianFile, err)
						}
					} else {
						obsidianFilePath = util.ObsidianPath(p.ObsidianFile)
						if obsidianFilePath != "" {
							contentToUse, err := readObsidianFile(obsidianFilePath)
							if err == nil {
								fileContentAmended := "FILENAME: " + p.ObsidianFile + "\n" + contentToUse
								p.UserInput = p.UserInput + "\nJournal File:\n" + fileContentAmended
								log.Printf("Added content from obsidian file: %s", obsidianFilePath)
							} else {
								log.Printf("Error reading obsidian file %s: %v", obsidianFilePath, err)
							}
						} else {
							log.Printf("[DEBUG] Obsidian file path could not be resolved for: %s", p.ObsidianFile)
						}
					}
				}

				log.Printf("[DEBUG] Attempting to get chatter for model: %s, vendor: %s", p.Model, p.Vendor)
				chatter, err := h.registry.GetChatter(p.Model, 2048, p.Vendor, "", false, false)
				if err != nil {
					log.Printf("Error creating chatter: %v", err)
					streamChan <- fmt.Sprintf("Error: %v", err)
					return
				}
				log.Printf("[DEBUG] Chatter created successfully.")

				chatReq := &domain.ChatRequest{
					Message: &chat.ChatCompletionMessage{
						Role:    "user",
						Content: p.UserInput,
					},
					PatternName:      p.PatternName,
					ContextName:      p.ContextName,
					PatternVariables: p.Variables, // Pass pattern variables
					Language:         request.Language,
					SessionName:      p.SessionName}
				log.Printf("[DEBUG] Constructed domain.ChatRequest: %+v", chatReq)

				opts := &domain.ChatOptions{
					Model:            p.Model,
					Temperature:      request.Temperature,
					TopP:             request.TopP,
					FrequencyPenalty: request.FrequencyPenalty,
					PresencePenalty:  request.PresencePenalty,
					Thinking:         request.Thinking,
				}
				log.Printf("[DEBUG] Constructed domain.ChatOptions: %+v", opts)

				session, err := chatter.Send(chatReq, opts)
				if err != nil {
					log.Printf("Error from chatter.Send: %v", err)
					streamChan <- fmt.Sprintf("Error: %v", err)
					return
				}
				log.Printf("[DEBUG] Received session from chatter.Send")

				if session == nil {
					log.Printf("No session returned from chatter.Send")
					streamChan <- "Error: No response from model"
					return
				}

				lastMsg := session.GetLastMessage()
				if lastMsg != nil {
					log.Printf("[DEBUG] Got last message from session, sending to stream channel.")
					streamChan <- lastMsg.Content
				} else {
					log.Printf("No message content in session")
					streamChan <- "Error: No response content"
				}
				log.Printf("[DEBUG] Goroutine finished for prompt: %+v", p)
			}(prompt)

			for content := range streamChan {
				select {
				case <-clientGone:
					log.Printf("[DEBUG] Client disconnected while streaming.")
					return
				default:
					log.Printf("[DEBUG] Received content from stream channel.")
					var response StreamResponse
					if strings.HasPrefix(content, "Error:") {
						log.Printf("[DEBUG] Content is an error: %s", content)
						response = StreamResponse{
							Type:    "error",
							Format:  "plain",
							Content: content,
						}
					} else {
						detectedFormat := detectFormat(content)
						log.Printf("[DEBUG] Detected format: %s", detectedFormat)
						response = StreamResponse{
							Type:    "content",
							Format:  detectedFormat,
							Content: content,
						}
					}
					log.Printf("[DEBUG] Writing SSE response: %+v", response)
					if err := writeSSEResponse(c.Writer, response); err != nil {
						log.Printf("Error writing response: %v", err)
						return
					}
				}
			}

			log.Printf("[DEBUG] Stream channel closed. Sending 'complete' response.")
			completeResponse := StreamResponse{
				Type:    "complete",
				Format:  "plain",
				Content: "",
			}
			if err := writeSSEResponse(c.Writer, completeResponse); err != nil {
				log.Printf("Error writing completion response: %v", err)
				return
			}
		}
	}
	log.Printf("[DEBUG] Exiting HandleChat handler normally.")
}

func readObsidianFile(path string) (string, error) {
	log.Printf("[DEBUG] Entering readObsidianFile for path: %s", path)
	fileContent, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[ERROR] Failed to read file %s: %v", path, err)
		return "", err
	}
	log.Printf("[DEBUG] Successfully read %d bytes from %s", len(fileContent), path)
	contentToUse := string(fileContent)
	monthsBackStr := os.Getenv("JOURNAL_FILE_RETRO_MONTHS")
	log.Printf("[DEBUG] JOURNAL_FILE_RETRO_MONTHS: '%s'", monthsBackStr)
	monthsBack, err := strconv.Atoi(monthsBackStr)
	if err != nil {
		log.Printf("[ERROR] Invalid JOURNAL_FILE_RETRO_MONTHS value: %v. Returning full file content.", err)
		return contentToUse, nil
	}
	log.Printf("[DEBUG] Retro months set to: %d", monthsBack)
	if dateRegex.MatchString(contentToUse) {
		log.Printf("[DEBUG] Date regex matched. Filtering content.")
		var sb strings.Builder
		lines := strings.Split(contentToUse, "\n")
		cutoffDate := time.Now().AddDate(0, -monthsBack, 0)
		log.Printf("[DEBUG] Filtering entries after %s", cutoffDate.Format("2006-01-02"))
		currentSectionIsRecent := true

		for _, line := range lines {
			isNewSectionHeader := false
			isNewSectionRecent := false

			matches := dateRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				dateStr := matches[1]
				lineDate, err := time.Parse("2006-01-02", dateStr)
				if err == nil {
					isNewSectionHeader = true
					isNewSectionRecent = !lineDate.Before(cutoffDate)
					log.Printf("[DEBUG] Found date '%s'. Is recent: %t", dateStr, isNewSectionRecent)
				}
			}

			if isNewSectionHeader {
				currentSectionIsRecent = isNewSectionRecent
			}

			if currentSectionIsRecent {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
		contentToUse = strings.TrimSuffix(sb.String(), "\n")
		log.Printf("[DEBUG] Filtered content length: %d", len(contentToUse))
	} else {
		log.Printf("[DEBUG] No dates found in file, using entire content.")
	}
	log.Printf("[DEBUG] Exiting readObsidianFile for path: %s", path)
	return contentToUse, nil
}

func readWeaviateJournal(prompt string, spec string) (string, string, error) {
	log.Printf("[DEBUG] Entering readWeaviateJournal. Spec: %s", spec)
	endpoint := os.Getenv("WEAVIATE_URL")
	certainty := os.Getenv("WEAVIATE_CERTAINTY")
	className := os.Getenv("WEAVIATE_CLASS")
	if endpoint == "" {
		return "", "", fmt.Errorf("WEAVIATE_URL not set")
	}
	if className == "" {
		return "", "", fmt.Errorf("WEAVIATE_CLASS not set")
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/v1/graphql"
	escapedPrompt := strings.ReplaceAll(prompt, `"`, `\"`)
	query := fmt.Sprintf(`{ Get { %s(limit: 1, hybrid: {query: "%s", alpha: %s}) { path content } } }`, className, escapedPrompt, certainty)
	payload := map[string]string{
		"query": query,
	}
	bs, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bs))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("weaviate returned status %d: %s", resp.StatusCode, string(body))
	}
	var parsed any
	path := ""
	content := ""
	if err := json.Unmarshal(body, &parsed); err == nil {
		if m, ok := parsed.(map[string]any); ok {
			if errs, ok := m["errors"]; ok {
				if arr, ok := errs.([]any); ok && len(arr) > 0 {
					var sbErr strings.Builder
					for _, e := range arr {
						b, _ := json.Marshal(e)
						sbErr.Write(b)
						sbErr.WriteString("\n")
					}
					return "", "", fmt.Errorf("weaviate graphql errors: %s", strings.TrimSpace(sbErr.String()))
				}
			}
			data, ok := m["data"].(map[string]any)
			if !ok {
				return "weaviate", "", fmt.Errorf("Data missing from output: " + string(body))
			}
			get, ok := data["Get"].(map[string]any)
			if !ok {
				return "weaviate", "", fmt.Errorf("Get missing from output: " + string(body))
			}
			if resultsRaw, ok := get[className]; ok {
				if arr, ok := resultsRaw.([]any); ok {
					for _, item := range arr {
						if itm, ok := item.(map[string]any); ok {
							if v, ok := itm["path"]; ok {
								path = v.(string)
							}
							if v, ok := itm["content"]; ok {
								content = v.(string)
							}
						}
					}
				}
			}
		}
	} else {
		return "", "", err
	}
	return path, content, nil
}

func (h *ChatHandler) StoreMessage(c *gin.Context) {
	log.Printf("[DEBUG] Entering StoreMessage handler")
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := c.BindJSON(&req); err != nil {
		log.Printf("[ERROR] StoreMessage: Error binding JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[DEBUG] StoreMessage request received.")
	if req.Prompt == "" {
		log.Printf("[ERROR] StoreMessage: prompt is required")
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}
	parsed := parseFilenameBlocks(req.Prompt)
	log.Printf("[DEBUG] Parsed %d filename blocks from prompt", len(parsed))
	var savedFilenames []string
	if len(parsed) > 0 {
		for filename, content := range parsed {
			filename = util.ObsidianPath(filename)
			log.Printf("[DEBUG] Storing content to filename: %s", filename)
			dir := filepath.Dir(filename)
			if dir != "" && dir != "." {
				log.Printf("[DEBUG] Creating directory: %s", dir)
				if err := os.MkdirAll(dir, 0755); err != nil {
					log.Printf("[ERROR] StoreMessage: Error creating directory %s: %v", dir, err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating directory: %v", err)})
					return
				}
			}
			if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
				log.Printf("[ERROR] StoreMessage: Error writing file %s: %v", filename, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error writing file %s: %v", filename, err)})
				return
			}
			log.Printf("Stored message content to %s", filename)
			savedFilenames = append(savedFilenames, filename)
		}
		c.JSON(http.StatusOK, gin.H{
			"message":   "Content stored successfully",
			"filenames": savedFilenames,
		})
	} else {
		log.Printf("[DEBUG] No FILENAME markers found in prompt.")
		c.JSON(http.StatusOK, gin.H{
			"message": "No FILENAME markers found; nothing stored",
		})
	}
	log.Printf("[DEBUG] Exiting StoreMessage handler")
}

func parseFilenameBlocks(input string) map[string]string {
	log.Printf("[DEBUG] Entering parseFilenameBlocks. Input length: %d", len(input))
	blocks := make(map[string]string)
	lines := strings.Split(input, "\n")
	var current string
	var buf []string
	for _, l := range lines {
		if strings.HasPrefix(l, "FILENAME:") {
			if current != "" {
				blocks[current] = strings.Join(buf, "\n")
				log.Printf("[DEBUG] Stored block for FILENAME: %s", current)
				buf = nil
			}
			current = strings.TrimSpace(strings.TrimPrefix(l, "FILENAME:"))
			log.Printf("[DEBUG] Found new FILENAME: %s", current)
		} else {
			if current != "" {
				buf = append(buf, l)
			}
		}
	}
	if current != "" {
		blocks[current] = strings.Join(buf, "\n")
		log.Printf("[DEBUG] Stored final block for FILENAME: %s", current)
	}
	log.Printf("[DEBUG] Exiting parseFilenameBlocks. Found %d blocks.", len(blocks))
	return blocks
}

func writeSSEResponse(w gin.ResponseWriter, response StreamResponse) error {
	log.Printf("[DEBUG] Entering writeSSEResponse for type: %s", response.Type)
	data, err := json.Marshal(response)
	if err != nil {
		log.Printf("[ERROR] Error marshaling SSE response: %v", err)
		return fmt.Errorf("error marshaling response: %v", err)
	}
	log.Printf("[DEBUG] Marshaled SSE data: %s", string(data))

	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(data)); err != nil {
		log.Printf("[ERROR] Error writing SSE response to client: %v", err)
		return fmt.Errorf("error writing response: %v", err)
	}

	w.(http.Flusher).Flush()
	log.Printf("[DEBUG] Flushed SSE response.")
	return nil
}

func detectFormat(content string) string {
	log.Printf("[DEBUG] Entering detectFormat")
	if strings.HasPrefix(content, "graph TD") ||
		strings.HasPrefix(content, "gantt") ||
		strings.HasPrefix(content, "flowchart") ||
		strings.HasPrefix(content, "sequenceDiagram") ||
		strings.HasPrefix(content, "classDiagram") ||
		strings.HasPrefix(content, "stateDiagram") {
		log.Printf("[DEBUG] Detected 'mermaid' format.")
		return "mermaid"
	}
	log.Printf("[DEBUG] Defaulting to 'markdown' format.")
	return "markdown"
}
