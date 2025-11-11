package util

import (
  "errors"
  "fmt"
  "os"
  "path/filepath"
  "regexp"
  "runtime"
  "strings"
  "time"

  "github.com/mmcdole/gofeed"
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

// GetDefaultConfigPath returns the default path for the configuration file
// if it exists, otherwise returns an empty string.
func GetDefaultConfigPath() (string, error) {
  homeDir, err := os.UserHomeDir()
  if err != nil {
    return "", fmt.Errorf("could not determine user home directory: %w", err)
  }

  defaultConfigPath := filepath.Join(homeDir, ".config", "fabric", "config.yaml")
  if _, err := os.Stat(defaultConfigPath); err != nil {
    if os.IsNotExist(err) {
      return "", nil // Return no error for non-existent config path
    }
    return "", fmt.Errorf("error accessing default config path: %w", err)
  }
  return defaultConfigPath, nil
}

var (
  // Updated to match all a tags with href attribute where href is not a local link
  linkRe = regexp.MustCompile(`(?i)(?s)<a\s+[^>]*href="([^#].*?)"[^>]*>(.*?)<\/a>`)
  //hRe           = regexp.MustCompile(`(?i)<h([1-6])>(.*?)</h[1-6]>`)
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
  // does not work for faz
  //html = hRe.ReplaceAllStringFunc(html, func(s string) string {
  //	parts := hRe.FindStringSubmatch(s)
  //	level, _ := strconv.Atoi(parts[1])
  //	content := parts[2]
  //	return strings.Repeat("#", level) + " " + content + "\n\n"
  //})
  html = linkRe.ReplaceAllStringFunc(html, func(s string) string {
    parts := linkRe.FindStringSubmatch(s)
    href := parts[1]
    text := parts[2]
    // replace line breaks with full stops
    text = strings.ReplaceAll(text, "\n", ".")
    text = strings.ReplaceAll(text, "\r", ".")

    return "[" + text + "](" + href + ")"
  })
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

// ConvertRSSFeedToMarkdown parses an RSS or Atom feed from the given URL
// and returns its contents as Obsidian-flavored Markdown with double quotes escaped.
func ConvertRSSFeedToMarkdown(url string) (string, error) {
  parser := gofeed.NewParser()
  feed, err := parser.ParseURL(url)
  if err != nil {
    return "", fmt.Errorf("failed to parse feed: %w", err)
  }

  var sb strings.Builder
  // Feed title as top-level heading
  if feed.Title != "" {
    sb.WriteString("# " + feed.Title + "\n\n")
  }

  for _, item := range feed.Items {
    // Item title as secondary heading
    if item.Title != "" {
      sb.WriteString("## " + item.Title + "\n")
    }
    // Published date if available
    published := time.Now()
    if item.PublishedParsed != nil {
      published = *item.PublishedParsed
    }
    sb.WriteString("> " + published.Format(time.RFC3339) + "\n\n")
    // Link to original item
    if item.Link != "" {
      sb.WriteString("[Original Link](" + item.Link + ")\n\n")
    }
    // Content or description
    content := item.Content
    if content == "" {
      content = item.Description
    }
    if content != "" {
      sb.WriteString(StripTags(content) + "\n\n")
    }
  }

  out := sb.String()
  out = strings.ReplaceAll(out, "\"", "") // escape all double quotes
  return out, nil
}

func ObsidianPath(obsidianFile string) string {
  basePath := os.Getenv("OBSIDIAN_BASE_PATH")
  return filepath.Join(basePath, obsidianFile)
}

func PathMap() map[string]string {
  basePath := os.Getenv("OBSIDIAN_BASE_PATH")
  vaultEnv := make(map[string]string)
  for _, ev := range os.Environ() {
    if strings.HasPrefix(ev, "OBSIDIAN_VAULT_PATH_") {
      partsEnv := strings.SplitN(ev, "=", 2)
      val := partsEnv[1]
      parts := strings.Split(val, "/")
      vaultEnv[parts[0]] = filepath.Join(basePath, val)
    }
  }
  return vaultEnv
}
