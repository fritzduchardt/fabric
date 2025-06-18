package fsdb

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/danielmiessler/fabric/common"
	"github.com/danielmiessler/fabric/plugins/template"
)

const inputSentinel = "__FABRIC_INPUT_SENTINEL_TOKEN__"

var (
	placeholderRe = regexp.MustCompile(`{{\s*[^{}\s]+\s*}}`)
)

type PatternsEntity struct {
	*StorageEntity
	SystemPatternFile      string
	UniquePatternsFilePath string
}

// Pattern represents a single pattern with its metadata
type Pattern struct {
	Name        string
	Description string
	Pattern     string
}

// GetApplyVariables main entry point for getting patterns from any source
func (o *PatternsEntity) GetApplyVariables(
	source string, variables map[string]string, input string) (pattern *Pattern, err error) {
	isFilePath := strings.HasPrefix(source, "\\") ||
		strings.HasPrefix(source, "/") ||
		strings.HasPrefix(source, "~") ||
		strings.HasPrefix(source, ".")

	if isFilePath {
		absPath, err := common.GetAbsolutePath(source)
		if err != nil {
			return nil, fmt.Errorf("could not resolve file path: %v", err)
		}
		pattern, err = o.getFromFile(absPath)
	} else {
		pattern, err = o.getFromDB(source)
	}

	if err != nil {
		return
	}

	err = o.applyVariables(pattern, variables, input)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}
	urlRegex := regexp.MustCompile(`https?://[^\s]+`)
	links := urlRegex.FindAllString(pattern.Pattern, -1)
	for _, link := range links {
		resp, err := client.Get(link)
		if err != nil {
			log.Printf("Error fetching URL %s: %v", link, err)
			continue
		}
		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Error reading response from URL %s: %v", link, err)
			continue
		}
		md := common.StripTags(string(body))
		pattern.Pattern = strings.ReplaceAll(pattern.Pattern, link, md)
	}

	return
}

func (o *PatternsEntity) applyVariables(
	pattern *Pattern, variables map[string]string, input string) (err error) {
	if !strings.Contains(pattern.Pattern, "{{input}}") {
		if !strings.HasSuffix(pattern.Pattern, "\n") {
			pattern.Pattern += "\n"
		}
		pattern.Pattern += "{{input}}"
	}

	withSentinel := strings.ReplaceAll(pattern.Pattern, "{{input}}", inputSentinel)

	var processed string
	if processed, err = template.ApplyTemplate(withSentinel, variables, ""); err != nil {
		return
	}

	if placeholderRe.MatchString(processed) {
		return fmt.Errorf("unresolved placeholders in pattern: %s", placeholderRe.FindString(processed))
	}

	pattern.Pattern = strings.ReplaceAll(processed, inputSentinel, input)
	return
}

// retrieves a pattern from the database by name
func (o *PatternsEntity) getFromDB(name string) (ret *Pattern, err error) {
	patternPath := filepath.Join(o.Dir, name, o.SystemPatternFile)

	var pattern []byte
	if pattern, err = os.ReadFile(patternPath); err != nil {
		return
	}

	patternStr := string(pattern)
	ret = &Pattern{
		Name:    name,
		Pattern: patternStr,
	}
	return
}

func (o *PatternsEntity) PrintLatestPatterns(latestNumber int) (err error) {
	var contents []byte
	if contents, err = os.ReadFile(o.UniquePatternsFilePath); err != nil {
		err = fmt.Errorf("could not read unique patterns file. Please run --updatepatterns (%s)", err)
		return
	}
	uniquePatterns := strings.Split(string(contents), "\n")
	if latestNumber > len(uniquePatterns) {
		latestNumber = len(uniquePatterns)
	}

	for i := len(uniquePatterns) - 1; i > len(uniquePatterns)-latestNumber-1; i-- {
		fmt.Println(uniquePatterns[i])
	}
	return
}

// reads a pattern from a file path and returns it
func (o *PatternsEntity) getFromFile(pathStr string) (pattern *Pattern, err error) {
	if strings.HasPrefix(pathStr, "~/") {
		var homedir string
		if homedir, err = os.UserHomeDir(); err != nil {
			err = fmt.Errorf("could not get home directory: %v", err)
			return
		}
		pathStr = filepath.Join(homedir, pathStr[2:])
	}

	var content []byte
	if content, err = os.ReadFile(pathStr); err != nil {
		err = fmt.Errorf("could not read pattern file %s: %v", pathStr, err)
		return
	}
	pattern = &Pattern{
		Name:    pathStr,
		Pattern: string(content),
	}
	return
}

// Get required for Storage interface
func (o *PatternsEntity) Get(name string) (*Pattern, error) {
	return o.GetApplyVariables(name, nil, "")
}
