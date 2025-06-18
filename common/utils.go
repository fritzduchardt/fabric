package common

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// GetAbsolutePath resolves a given path to its absolute form, handling ~, ./, ../, UNC paths, and symlinks.
func GetAbsolutePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}

	// Handle UNC paths on Windows
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `\\`) {
		return path, nil
	}

	// Handle ~ for home directory expansion
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("could not resolve home directory")
		}
		path = filepath.Join(home, path[1:])
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("could not get absolute path")
	}

	// Resolve symlinks, but allow non-existent paths
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return resolvedPath, nil
	}
	if os.IsNotExist(err) {
		// Return the absolute path for non-existent paths
		return absPath, nil
	}

	return "", fmt.Errorf("could not resolve symlinks: %w", err)
}

// Helper function to check if a symlink points to a directory
func IsSymlinkToDir(path string) bool {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return false
	}

	if fileInfo.Mode()&os.ModeSymlink != 0 {
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false
		}

		fileInfo, err = os.Stat(resolvedPath)
		if err != nil {
			return false
		}

		return fileInfo.IsDir()
	}

	return false // Regular directories should not be treated as symlinks
}

var (
	linkRe        = regexp.MustCompile(`(?i)<a\s+href="([^"]+)">(.*?)</a>`)
	hRe           = regexp.MustCompile(`(?i)<h([1-6])>(.*?)</h[1-6]>`)
	strongRe      = regexp.MustCompile(`(?i)<(?:strong|b)>(.*?)</(?:strong|b)>`)
	emRe          = regexp.MustCompile(`(?i)<(?:em|i)>(.*?)</(?:em|i)>`)
	liRe          = regexp.MustCompile(`(?i)<li>(.*?)</li>`)
	pRe           = regexp.MustCompile(`(?i)<p>(.*?)</p>`)
	brRe          = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	scriptStyleRe = regexp.MustCompile(`(?is)<(?:script|style|link|meta)[^>]*>.*?</(?:script|style)>`)
)

// StripTags converts HTML to Markdown by removing HTML tags and formatting the content
func StripTags(html string) string {
	html = scriptStyleRe.ReplaceAllString(html, "")
	html = hRe.ReplaceAllStringFunc(html, func(s string) string {
		parts := hRe.FindStringSubmatch(s)
		level, _ := strconv.Atoi(parts[1])
		content := parts[2]
		return strings.Repeat("#", level) + " " + content + "\n\n"
	})
	html = linkRe.ReplaceAllString(html, "[$2]($1)")
	html = strongRe.ReplaceAllString(html, "**$1**")
	html = emRe.ReplaceAllString(html, "*$1*")
	html = liRe.ReplaceAllString(html, "- $1\n")
	html = pRe.ReplaceAllString(html, "$1\n\n")
	html = brRe.ReplaceAllString(html, "\n")
	html = tagRe.ReplaceAllString(html, "")

	// remove lines with up to three words, keep empty lines
	lines := strings.Split(html, "\n")
	var kept []string
	for _, line := range lines {
		words := strings.Fields(line)
		if len(words) == 0 || len(words) > 3 {
			kept = append(kept, line)
		}
	}
	html = strings.Join(kept, "\n")
	// normalize multiple consecutive newlines to two newlines
	html = regexp.MustCompile(`\s{2,}`).ReplaceAllString(html, "\n\n")

	return html
}
