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
	StrategyName string            `json:"strategyName"`
	Variables    map[string]string `json:"variables,omitempty"`
	ObsidianFile string            `json:"obsidianFile"`
	SessionName  string            `json:"sessionName"`
}

type ChatRequest struct {
	Prompts            []PromptRequest `json:"prompts"`
	Language           string          `json:"language"`
	ModelContextLength int             `json:"modelContextLength,omitempty"` // Context window size
	domain.ChatOptions                 // Embed the ChatOptions from common package
}

type StreamResponse struct {
	Type    string `json:"type"`
	Format  string `json:"format"`
	Content string `json:"content"`
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

func (h *ChatHandler) addLinks(str string) string {
	links := urlRegex.FindAllString(str, -1)
	totalLinkChars := 0
	const maxLinkChars = 500000
	for _, link := range links {
		if strings.HasSuffix(link, ".rss") || strings.HasSuffix(link, ".xml") || strings.HasSuffix(link, ".xml#") {
			md, err := util.ConvertRSSFeedToMarkdown(link)
			if totalLinkChars+len(md) > maxLinkChars {
				continue
			}
			totalLinkChars += len(md)
			if err == nil {
				str = str + "\nRSS Feed Markdown:\n" + md
			}
			continue
		}
		if totalLinkChars >= maxLinkChars {
			break
		}
		resp, err := http.Get(link)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		text := util.StripTags(string(body))
		if totalLinkChars+len(text) > maxLinkChars {
			continue
		}
		content := "URL: " + link + "\n" + text
		str = str + "\nURL Content:\n" + content
		totalLinkChars += len(text)
	}
	return str
}

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
		contextDir := filepath.Join(fabricHome, "contexts")
		if err := os.MkdirAll(contextDir, 0755); err != nil {
			log.Printf("Error creating context directory %s: %v", contextDir, err)
		} else {
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
			now := time.Now()
			personalName := os.Getenv("PERSONAL_NAME")
			birthYear := os.Getenv("BIRTH_YEAR")
			gender := os.Getenv("GENDER")
			currentModel := strings.ReplaceAll(request.Prompts[0].Model, ".", "-")
			if personalName == "" {
				personalName = "Fritz"
				birthYear = "1976"
				gender = "male"
			}
			filename := filepath.Join(contextDir, "general_context.md")
			content := fmt.Sprintf("# CONTEXT\n\nCurrent model: %s\nUser name: %s\nBirth Year: %s\nGender: %s\nCurrent Date: %s\nPattern name(s): %s\nThis is not a chat\n", currentModel, personalName, birthYear, gender, now.Format("2006-01-02"), patternNames)
			if err := os.WriteFile(filename, []byte(content), 0666); err != nil {
				log.Printf("Error writing context file %s: %v", filename, err)
			}
		}
	}
	log.Printf("Received chat request - Language: '%s', Prompts: %d", request.Language, len(request.Prompts))
	c.Writer.Header().Set("Content-Type", "text/readystream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	clientGone := c.Writer.CloseNotify()
	for _, prompt := range request.Prompts {
		select {
		case <-clientGone:
			log.Printf("Client disconnected")
			return
		default:
			streamChan := make(chan string)
			go func(p PromptRequest) {
				defer close(streamChan)
				p.UserInput = h.addLinks(p.UserInput)
				if p.ObsidianFile == "" && p.PatternName == "protocol" {
					paths, contents, err := readWeaviateJournal(p.UserInput)
					if err == nil {
						for idx, path := range paths {
							contentToUse := contents[idx]
							fileContentAmended := "FILENAME: " + path + "\n" + contentToUse
							p.UserInput = p.UserInput + "\nJournal File:\n" + fileContentAmended
							log.Printf("[INFO] Added content from weaviate (path: %s)", path)
						}
					}
				} else if p.ObsidianFile != "" {
					obsidianFilePath := util.ObsidianPath(p.ObsidianFile)
					if obsidianFilePath != "" {
						contentToUse, err := readObsidianFile(obsidianFilePath)
						if err == nil {
							if strings.Contains(contentToUse, "#followlinks") {
								contentToUse = h.addLinks(contentToUse)
							}
							fileContentAmended := "FILENAME: " + p.ObsidianFile + "\n" + contentToUse
							p.UserInput = p.UserInput + "\nJournal File:\n" + fileContentAmended
						}
					}
				}
				chatter, err := h.registry.GetChatter(p.Model, request.ModelContextLength, p.Vendor, "", false, false)
				if err != nil {
					streamChan <- fmt.Sprintf("Error: %v", err)
					return
				}
				chatReq := &domain.ChatRequest{
					Message:          &chat.ChatCompletionMessage{Role: "user", Content: p.UserInput},
					PatternName:      p.PatternName,
					ContextName:      p.ContextName,
					PatternVariables: p.Variables,
					Language:         request.Language,
					SessionName:      p.SessionName,
				}
				opts := &domain.ChatOptions{
					Model:            p.Model,
					Temperature:      request.Temperature,
					TopP:             request.TopP,
					FrequencyPenalty: request.FrequencyPenalty,
					PresencePenalty:  request.PresencePenalty,
					Thinking:         request.Thinking,
				}
				session, err := chatter.Send(chatReq, opts)
				if err != nil {
					streamChan <- fmt.Sprintf("Error: %v", err)
					return
				}
				if session == nil {
					streamChan <- "Error: No response from model"
					return
				}
				lastMsg := session.GetLastMessage()
				if lastMsg != nil {
					streamChan <- lastMsg.Content
				} else {
					streamChan <- "Error: No response content"
				}
			}(prompt)
			for content := range streamChan {
				select {
				case <-clientGone:
					return
				default:
					var response StreamResponse
					if strings.HasPrefix(content, "Error:") {
						response = StreamResponse{Type: "error", Format: "plain", Content: content}
					} else {
						detectedFormat := detectFormat(content)
						response = StreamResponse{Type: "content", Format: detectedFormat, Content: content}
					}
					if err := writeSSEResponse(c.Writer, response); err != nil {
						return
					}
				}
			}
			completeResponse := StreamResponse{Type: "complete", Format: "plain", Content: ""}
			if err := writeSSEResponse(c.Writer, completeResponse); err != nil {
				return
			}
		}
	}
}

func readObsidianFile(path string) (string, error) {
	log.Printf("[DEBUG] Entering readObsidianFile for path: %s", path)
	fileContent, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[ERROR] Failed to read file %s: %v", path, err)
		return "", err
	}
	contentToUse := string(fileContent)
	monthsBackStr := os.Getenv("JOURNAL_FILE_RETRO_MONTHS")
	monthsBack, err := strconv.Atoi(monthsBackStr)
	if err != nil {
		return contentToUse, nil
	}
	if dateRegex.MatchString(contentToUse) {
		var sb strings.Builder
		lines := strings.Split(contentToUse, "\n")
		cutoffDate := time.Now().AddDate(0, -monthsBack, 0)
		currentSectionIsRecent := true
		for _, line := range lines {
			isNewSectionHeader := false
			isNewSectionRecent := false
			matches := dateRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				if lineDate, err := time.Parse("2006-01-02", matches[1]); err == nil {
					isNewSectionHeader = true
					isNewSectionRecent = !lineDate.Before(cutoffDate)
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
	}
	return contentToUse, nil
}

func readWeaviateJournal(prompt string) ([]string, []string, error) {
	log.Printf("[DEBUG] Entering readWeaviateJournal")
	endpoint := os.Getenv("WEAVIATE_URL")
	certainty := os.Getenv("WEAVIATE_CERTAINTY")
	className := os.Getenv("WEAVIATE_CLASS")
	if endpoint == "" {
		return nil, nil, fmt.Errorf("WEAVIATE_URL not set")
	}
	if className == "" {
		return nil, nil, fmt.Errorf("WEAVIATE_CLASS not set")
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/v1/graphql"
	escapedPrompt := strings.ReplaceAll(prompt, `"`, `\"`)
	query := fmt.Sprintf(`{ Get { %s(limit: 1, hybrid: {query: "%s", alpha: %s}) { path content } } }`, className, escapedPrompt, certainty)
	payload := map[string]string{"query": query}
	bs, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bs))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("weaviate returned status %d: %s", resp.StatusCode, string(body))
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, err
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("invalid response format: %s", string(body))
	}
	if errs, ok := m["errors"]; ok {
		if arr, ok := errs.([]any); ok && len(arr) > 0 {
			var sbErr strings.Builder
			for _, e := range arr {
				b, _ := json.Marshal(e)
				sbErr.Write(b)
				sbErr.WriteString("\n")
			}
			return nil, nil, fmt.Errorf("weaviate graphql errors: %s", strings.TrimSpace(sbErr.String()))
		}
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("Data missing from output: %s", string(body))
	}
	get, ok := data["Get"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("Get missing from output: %s", string(body))
	}
	resultsRaw, ok := get[className]
	if !ok {
		return nil, nil, fmt.Errorf("Results missing for class %s", className)
	}
	arr, ok := resultsRaw.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("Unexpected result type for class %s", className)
	}
	var paths []string
	var contents []string
	for _, item := range arr {
		if itm, ok := item.(map[string]any); ok {
			var p, c string
			if v, ok := itm["path"]; ok {
				if ps, ok := v.(string); ok {
					p = ps
				}
			}
			if v, ok := itm["content"]; ok {
				if cs, ok := v.(string); ok {
					c = cs
				}
			}
			paths = append(paths, p)
			contents = append(contents, c)
		}
	}
	return paths, contents, nil
}

func (h *ChatHandler) StoreMessage(c *gin.Context) {
	log.Printf("[DEBUG] Entering StoreMessage handler")
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}
	parsed := parseFilenameBlocks(req.Prompt)
	var savedFilenames []string
	if len(parsed) > 0 {
		for filename, content := range parsed {
			filename = util.ObsidianPath(filename)
			dir := filepath.Dir(filename)
			if dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0755); err != nil {
					log.Printf("[ERROR] " + fmt.Sprintf("Error creating directory: %v", err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating directory: %v", err)})
					return
				}
			}
			if err := os.WriteFile(filename, []byte(content), 0666); err != nil {
				log.Printf("[ERROR] " + fmt.Sprintf("Error writing file %s: %v", filename, err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error writing file %s: %v", filename, err)})
				return
			}
			savedFilenames = append(savedFilenames, filename)
		}
		c.JSON(http.StatusOK, gin.H{"message": "Content stored successfully", "filenames": savedFilenames})
	} else {
		c.JSON(http.StatusOK, gin.H{"message": "No FILENAME markers found; nothing stored"})
	}
}

func parseFilenameBlocks(input string) map[string]string {
	blocks := make(map[string]string)
	lines := strings.Split(input, "\n")
	var current string
	var buf []string
	for _, l := range lines {
		if strings.HasPrefix(l, "FILENAME:") {
			if current != "" {
				blocks[current] = strings.Join(buf, "\n")
				buf = nil
			}
			current = strings.TrimSpace(strings.TrimPrefix(l, "FILENAME:"))
		} else {
			if current != "" {
				buf = append(buf, l)
			}
		}
	}
	if current != "" {
		blocks[current] = strings.Join(buf, "\n")
	}
	return blocks
}

func writeSSEResponse(w gin.ResponseWriter, response StreamResponse) error {
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("error marshaling response: %v", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(data)); err != nil {
		return fmt.Errorf("error writing response: %v", err)
	}
	w.(http.Flusher).Flush()
	return nil
}

func detectFormat(content string) string {
	if strings.HasPrefix(content, "graph TD") ||
		strings.HasPrefix(content, "gantt") ||
		strings.HasPrefix(content, "flowchart") ||
		strings.HasPrefix(content, "sequenceDiagram") ||
		strings.HasPrefix(content, "classDiagram") ||
		strings.HasPrefix(content, "stateDiagram") {
		return "mermaid"
	}
	return "markdown"
}
