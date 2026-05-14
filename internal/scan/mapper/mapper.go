// Package mapper builds a unified repository map by combining symbol extraction
// (ctags / tree-sitter CLI), LOC counting (cloc), and file-hash caching.
package mapper

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/ai"
)

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

// Symbol represents a named code entity extracted from the repository.
type Symbol struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`      // function, class, method, interface, import
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Language string `json:"language"`
}

// FileStats holds per-file line-count statistics.
type FileStats struct {
	Language string `json:"language"`
	Code     int    `json:"code"`
	Comment  int    `json:"comment"`
	Blank    int    `json:"blank"`
}

// RepoMap is the unified output of the mapping process.
type RepoMap struct {
	Symbols     []Symbol             `json:"symbols"`
	FileStats   map[string]FileStats `json:"file_stats"`
	FileHashes  map[string]string    `json:"file_hashes"`
	LOCSummary  map[string]int       `json:"loc_summary"`
	TotalFiles  int                  `json:"total_files"`
	TotalLOC    int                  `json:"total_loc"`
	Annotations []FileAnnotation     `json:"annotations,omitempty"`
}

// MapConfig configures a mapping run.
type MapConfig struct {
	RepoPath     string
	ExcludePaths []string
	PrevHashes   map[string]string // previous hashes for incremental mapping
	AIProvider   ai.Provider       // optional; enables AI semantic annotation when set
	Ctx          context.Context   // optional; used for AI calls; defaults to context.Background()
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// BuildMap is the main entry point. It computes file hashes, runs cloc (or a
// fallback), runs ctags (if available), and assembles a RepoMap.
func BuildMap(cfg MapConfig) (*RepoMap, error) {
	repoPath, err := filepath.Abs(cfg.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("mapper: resolve repo path: %w", err)
	}

	// 1. File hashes (always available — no external tool).
	hashes, err := ComputeFileHashes(repoPath, cfg.ExcludePaths)
	if err != nil {
		return nil, fmt.Errorf("mapper: compute file hashes: %w", err)
	}

	// Determine which files to process. When previous hashes are supplied we
	// can restrict symbol extraction and LOC counting to changed files, but
	// the full hash map is always returned so callers can persist it.
	filesToProcess := ChangedFiles(cfg.PrevHashes, hashes)

	// 2. LOC counting.
	fileStats, locSummary, err := RunCloc(repoPath)
	if err != nil {
		// cloc unavailable or failed — fall back to simple counting.
		fileStats, locSummary, err = fallbackLOC(repoPath, cfg.ExcludePaths)
		if err != nil {
			return nil, fmt.Errorf("mapper: fallback LOC: %w", err)
		}
	}

	// 3. Symbol extraction (ctags, then tree-sitter CLI).
	var symbols []Symbol

	if ToolAvailable("ctags") || ToolAvailable("universal-ctags") {
		syms, ctagsErr := RunCtags(repoPath)
		if ctagsErr == nil {
			symbols = append(symbols, syms...)
		}
	}

	if ToolAvailable("tree-sitter") {
		syms, tsErr := runTreeSitter(repoPath, filesToProcess)
		if tsErr == nil {
			symbols = append(symbols, syms...)
		}
	}

	// Deduplicate symbols by (Name, FilePath, Line).
	symbols = deduplicateSymbols(symbols)

	// 4. Assemble result.
	totalLOC := 0
	for _, v := range locSummary {
		totalLOC += v
	}

	rm := &RepoMap{
		Symbols:    symbols,
		FileStats:  fileStats,
		FileHashes: hashes,
		LOCSummary: locSummary,
		TotalFiles: len(hashes),
		TotalLOC:   totalLOC,
	}

	// 5. Optional AI semantic annotation.
	if cfg.AIProvider != nil {
		ctx := cfg.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		annotations, _ := AnnotateFiles(ctx, cfg.AIProvider, rm, repoPath)
		rm.Annotations = annotations
	}

	return rm, nil
}

// ComputeFileHashes walks every file under repoPath (honouring excludePaths)
// and returns a map of relative-path -> SHA-256 hex digest.
func ComputeFileHashes(repoPath string, excludePaths []string) (map[string]string, error) {
	hashes := make(map[string]string)

	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}

		if shouldExclude(rel, excludePaths) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		h, hashErr := hashFile(path)
		if hashErr != nil {
			return nil // skip files we cannot read
		}
		hashes[rel] = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hashes, nil
}

// ChangedFiles returns the list of file paths that are new, deleted, or
// modified between oldHashes and newHashes.
func ChangedFiles(oldHashes, newHashes map[string]string) []string {
	seen := make(map[string]struct{})
	var changed []string

	for path, newHash := range newHashes {
		oldHash, exists := oldHashes[path]
		if !exists || oldHash != newHash {
			changed = append(changed, path)
			seen[path] = struct{}{}
		}
	}
	// Deleted files.
	for path := range oldHashes {
		if _, exists := newHashes[path]; !exists {
			if _, already := seen[path]; !already {
				changed = append(changed, path)
			}
		}
	}

	sort.Strings(changed)
	return changed
}

// RunCloc shells out to `cloc --json <repoPath>` and parses the JSON output
// into per-file stats and a language LOC summary.
func RunCloc(repoPath string) (map[string]FileStats, map[string]int, error) {
	if !ToolAvailable("cloc") {
		return nil, nil, fmt.Errorf("cloc not found in PATH")
	}

	cmd := exec.Command("cloc", "--json", "--by-file", repoPath)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("cloc exec: %w", err)
	}

	// cloc --json --by-file produces a JSON object with:
	//   "header": { ... },
	//   "SUM": { ... },
	//   "<filepath>": { "language": "Go", "code": 123, "comment": 4, "blank": 5 },
	//   ...
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, nil, fmt.Errorf("cloc json: %w", err)
	}

	type clocEntry struct {
		Language string `json:"language"`
		Code     int    `json:"code"`
		Comment  int    `json:"comment"`
		Blank    int    `json:"blank"`
	}

	fileStats := make(map[string]FileStats)
	locSummary := make(map[string]int)

	for key, val := range raw {
		if key == "header" || key == "SUM" {
			continue
		}
		var entry clocEntry
		if err := json.Unmarshal(val, &entry); err != nil {
			continue
		}

		rel, relErr := filepath.Rel(repoPath, key)
		if relErr != nil {
			rel = key
		}

		fileStats[rel] = FileStats(entry)
		locSummary[entry.Language] += entry.Code
	}

	return fileStats, locSummary, nil
}

// RunCtags shells out to ctags (universal-ctags preferred) and parses the
// output into a slice of Symbol values.
func RunCtags(repoPath string) ([]Symbol, error) {
	binary := "ctags"
	if ToolAvailable("universal-ctags") {
		binary = "universal-ctags"
	} else if !ToolAvailable("ctags") {
		return nil, fmt.Errorf("ctags not found in PATH")
	}

	// Use JSON output if universal-ctags, otherwise fall back to default
	// tab-separated format.
	args := []string{
		"-R",
		"--fields=+Kln",
		"--output-format=json",
		repoPath,
		"-f", "-", // write to stdout
	}

	cmd := exec.Command(binary, args...)
	out, err := cmd.Output()
	if err != nil {
		// Retry without --output-format=json (older ctags).
		return runCtagsTabular(binary, repoPath)
	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	}

	return parseCtagsJSON(out, repoPath)
}

// ToolAvailable returns true when the named binary exists in PATH.
func ToolAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ---------------------------------------------------------------------------
// Internal helpers — ctags
// ---------------------------------------------------------------------------

// parseCtagsJSON parses universal-ctags JSON-lines output.
func parseCtagsJSON(data []byte, repoPath string) ([]Symbol, error) {
	var symbols []Symbol

	type ctagsJSONEntry struct {
		Type     string `json:"_type"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		Line     int    `json:"line"`
		Kind     string `json:"kind"`
		Language string `json:"language"`
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry ctagsJSONEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "tag" {
			continue
		}

		rel, relErr := filepath.Rel(repoPath, entry.Path)
		if relErr != nil {
			rel = entry.Path
		}

		symbols = append(symbols, Symbol{
			Name:     entry.Name,
			Kind:     normalizeKind(entry.Kind),
			FilePath: rel,
			Line:     entry.Line,
			Language: entry.Language,
		})
	}
	return symbols, nil
}

// runCtagsTabular falls back to the classic ctags tab-separated format.
func runCtagsTabular(binary, repoPath string) ([]Symbol, error) {
	args := []string{
		"-R",
		"--fields=+Kln",
		repoPath,
		"-f", "-",
	}
	cmd := exec.Command(binary, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ctags tabular exec: %w", err)
	}

// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input

// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command

	var symbols []Symbol
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "!_") {
			continue // skip header lines
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}

		name := parts[0]
		filePath := parts[1]

		rel, relErr := filepath.Rel(repoPath, filePath)
		if relErr != nil {
			rel = filePath
		}

		kind := ""
		lang := ""
		lineNum := 0

		// Parse extended fields (key:value pairs after the pattern).
		for _, field := range parts[3:] {
			if k, v, ok := strings.Cut(field, ":"); ok {
				switch k {
				case "kind":
					kind = v
				case "language":
					lang = v
				case "line":
					if n, err := strconv.Atoi(v); err == nil {
						lineNum = n
					}
				}
			} else {
				// Bare field is the kind letter in short format.
				kind = field
			}
		}

		symbols = append(symbols, Symbol{
			Name:     name,
			Kind:     normalizeKind(kind),
			FilePath: rel,
			Line:     lineNum,
			Language: lang,
		})
	}
	return symbols, nil
}

// ---------------------------------------------------------------------------
// Internal helpers — tree-sitter CLI
// ---------------------------------------------------------------------------

// runTreeSitter shells out to the tree-sitter CLI for symbol extraction.
// It parses `tree-sitter parse <file>` S-expression output looking for
// top-level declarations and reads the source file to resolve identifier
// names from the byte positions reported by tree-sitter.
func runTreeSitter(repoPath string, files []string) ([]Symbol, error) {
	if !ToolAvailable("tree-sitter") {
		return nil, fmt.Errorf("tree-sitter not found in PATH")
	}

	var symbols []Symbol
	for _, rel := range files {
		abs := filepath.Join(repoPath, rel)

		cmd := exec.Command("tree-sitter", "parse", abs)
		out, err := cmd.Output()
		if err != nil {
			continue // skip unparseable files
		}

// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input

		// Read source file lines so we can resolve identifier text from
		// row/column positions reported in the S-expression.
		srcBytes, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		srcLines := strings.Split(string(srcBytes), "\n")

// #nosec G304 -- reads tool-output JSON inside the artifact dir

		lang := guessLanguage(rel)
		syms := parseTreeSitterOutput(string(out), rel, lang, srcLines)
		symbols = append(symbols, syms...)
	}
	return symbols, nil
}

// parseTreeSitterOutput does a best-effort extraction of declaration nodes
// from the S-expression output of `tree-sitter parse`.
//
// The S-expression is hierarchical. A declaration node appears on one line
// with its range, and its child `name: (identifier) [row, col] - [row2, col2]`
// appears on a subsequent indented line. We track the most recent declaration
// node and, when we see a `name: (identifier)` child, use the row/col
// positions to read the actual identifier text from the source file lines.
//
// Example:
//
//	(function_declaration [0, 0] - [5, 1]
//	  name: (identifier) [0, 5] - [0, 9]
//	  ...
func parseTreeSitterOutput(sexp, filePath, language string, srcLines []string) []Symbol {
	var symbols []Symbol

	// Map of tree-sitter node types to our Symbol kinds.
	kindMap := map[string]string{
		"function_declaration":  "function",
		"function_definition":   "function",
		"method_declaration":    "method",
		"method_definition":     "method",
		"class_declaration":     "class",
		"class_definition":      "class",
		"interface_declaration": "interface",
		"import_declaration":    "import",
		"import_statement":      "import",
		"type_declaration":      "class",
		"struct_specifier":      "class",
		"function_item":         "function",
		"impl_item":             "class",
		"trait_item":            "interface",
	}

	// Track current declaration context.
	var currentKind string
	var declLine int

	scanner := bufio.NewScanner(strings.NewReader(sexp))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Check if this line starts a declaration node.
		for nodeType, kind := range kindMap {
			if strings.Contains(trimmed, "("+nodeType) {
				currentKind = kind
				declLine = extractTSLine(trimmed)
				break
			}
		}

		// Check if this line has a name: (identifier) child with positions.
		if currentKind != "" && strings.Contains(trimmed, "name: (identifier)") {
			name := extractTSNameFromSource(trimmed, srcLines)
			if name != "" {
				symbols = append(symbols, Symbol{
					Name:     name,
					Kind:     currentKind,
					FilePath: filePath,
					Line:     declLine,
					Language: language,
				})
			}
			currentKind = ""
		}
	}
	return symbols
}

// extractTSNameFromSource reads the identifier text from source lines using
// the row/column positions in a tree-sitter S-expression line like:
//
//	name: (identifier) [0, 5] - [0, 9]
//
// The positions are [startRow, startCol] - [endRow, endCol] (0-based).
func extractTSNameFromSource(line string, srcLines []string) string {
	// Find the position annotation after "name: (identifier)".
	idx := strings.Index(line, "name: (identifier)")
	if idx == -1 {
		return ""
	}
	rest := line[idx+len("name: (identifier)"):]

	startRow, startCol, endRow, endCol, ok := parseTSRange(rest)
	if !ok {
		return ""
	}

	if startRow < 0 || startRow >= len(srcLines) || endRow < 0 || endRow >= len(srcLines) {
		return ""
	}

	if startRow == endRow {
		src := srcLines[startRow]
		if startCol > len(src) {
			startCol = len(src)
		}
		if endCol > len(src) {
			endCol = len(src)
		}
		if startCol >= endCol {
			return ""
		}
		return src[startCol:endCol]
	}

	// Multi-line identifier (unusual, but handle it).
	var b strings.Builder
	for row := startRow; row <= endRow; row++ {
		if row >= len(srcLines) {
			break
		}
		src := srcLines[row]
		sc := 0
		ec := len(src)
		if row == startRow {
			sc = startCol
		}
		if row == endRow {
			ec = endCol
		}
		if sc > len(src) {
			sc = len(src)
		}
		if ec > len(src) {
			ec = len(src)
		}
		b.WriteString(src[sc:ec])
	}
	return b.String()
}

// parseTSRange parses a tree-sitter range annotation like " [0, 5] - [0, 9]"
// and returns (startRow, startCol, endRow, endCol, ok).
func parseTSRange(s string) (int, int, int, int, bool) {
	// Find first "[row, col]"
	i1 := strings.Index(s, "[")
	if i1 == -1 {
		return 0, 0, 0, 0, false
	}
	j1 := strings.Index(s[i1:], "]")
	if j1 == -1 {
		return 0, 0, 0, 0, false
	}
	startParts := strings.Split(s[i1+1:i1+j1], ",")
	if len(startParts) != 2 {
		return 0, 0, 0, 0, false
	}

	startRow, err1 := strconv.Atoi(strings.TrimSpace(startParts[0]))
	startCol, err2 := strconv.Atoi(strings.TrimSpace(startParts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, 0, 0, false
	}

	// Find second "[row, col]" after the dash.
	rest := s[i1+j1+1:]
	i2 := strings.Index(rest, "[")
	if i2 == -1 {
		return 0, 0, 0, 0, false
	}
	j2 := strings.Index(rest[i2:], "]")
	if j2 == -1 {
		return 0, 0, 0, 0, false
	}
	endParts := strings.Split(rest[i2+1:i2+j2], ",")
	if len(endParts) != 2 {
		return 0, 0, 0, 0, false
	}

	endRow, err3 := strconv.Atoi(strings.TrimSpace(endParts[0]))
	endCol, err4 := strconv.Atoi(strings.TrimSpace(endParts[1]))
	if err3 != nil || err4 != nil {
		return 0, 0, 0, 0, false
	}

	return startRow, startCol, endRow, endCol, true
}

// extractTSLine pulls the starting line number from a tree-sitter position
// annotation like `[12, 0]`.
func extractTSLine(line string) int {
	idx := strings.Index(line, "[")
	if idx == -1 {
		return 0
	}
	end := strings.Index(line[idx:], ",")
	if end == -1 {
		return 0
	}
	numStr := strings.TrimSpace(line[idx+1 : idx+end])
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0
	}
	return n + 1 // tree-sitter uses 0-based lines
}

// ---------------------------------------------------------------------------
// Internal helpers — fallback LOC counting
// ---------------------------------------------------------------------------

// fallbackLOC counts lines by walking the repo and reading each file.
func fallbackLOC(repoPath string, excludePaths []string) (map[string]FileStats, map[string]int, error) {
	fileStats := make(map[string]FileStats)
	locSummary := make(map[string]int)

	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}

		if shouldExclude(rel, excludePaths) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		lang := guessLanguage(rel)
		if lang == "" {
			return nil // skip unknown file types
		}

		code, blank, err := countLines(path)
		if err != nil {
			return nil
		}

		fileStats[rel] = FileStats{
			Language: lang,
			Code:     code,
			Comment:  0, // simple fallback cannot distinguish comments
			Blank:    blank,
		}
		locSummary[lang] += code
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return fileStats, locSummary, nil
}

// countLines returns (non-blank lines, blank lines) for a file.
func countLines(path string) (code int, blank int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

// #nosec G304 -- reads tool-output JSON inside the artifact dir

	scanner := bufio.NewScanner(f)
	// Increase buffer size for long lines.
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			blank++
		} else {
			code++
		}
	}
	return code, blank, scanner.Err()
}

// ---------------------------------------------------------------------------
// Internal helpers — utilities
// ---------------------------------------------------------------------------

// hashFile returns the hex-encoded SHA-256 digest of the file at path.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

// #nosec G304 -- reads tool-output JSON inside the artifact dir

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// shouldExclude returns true when rel matches one of the exclude patterns.
// Patterns may be directory prefixes or filepath.Match globs.
func shouldExclude(rel string, excludePaths []string) bool {
	for _, pat := range excludePaths {
		// Direct prefix match (directory exclusion).
		if strings.HasPrefix(rel, pat) {
			return true
		}
		// Glob match.
		if matched, _ := filepath.Match(pat, rel); matched {
			return true
		}
		// Also test the base name.
		if matched, _ := filepath.Match(pat, filepath.Base(rel)); matched {
			return true
		}
	}
	return false
}

// normalizeKind maps ctags kind strings/letters to our canonical kinds.
func normalizeKind(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "f", "func", "function", "function_declaration", "function_definition":
		return "function"
	case "c", "class", "class_declaration", "struct", "type":
		return "class"
	case "m", "method", "member":
		return "method"
	case "i", "interface":
		return "interface"
	case "import", "imported":
		return "import"
	case "v", "variable", "var":
		return "variable"
	case "p", "package":
		return "package"
	case "t", "typedef":
		return "class"
	default:
		if raw == "" {
			return "unknown"
		}
		return raw
	}
}

// guessLanguage returns a language name based on the file extension.
func guessLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "Go"
	case ".py":
		return "Python"
	case ".js":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".tsx":
		return "TypeScript"
	case ".jsx":
		return "JavaScript"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".c":
		return "C"
	case ".h":
		return "C Header"
	case ".cpp", ".cc", ".cxx":
		return "C++"
	case ".hpp", ".hxx":
		return "C++ Header"
	case ".cs":
		return "C#"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".swift":
		return "Swift"
	case ".kt", ".kts":
		return "Kotlin"
	case ".scala":
		return "Scala"
	case ".sh", ".bash", ".zsh":
		return "Shell"
	case ".yaml", ".yml":
		return "YAML"
	case ".json":
		return "JSON"
	case ".toml":
		return "TOML"
	case ".xml":
		return "XML"
	case ".html", ".htm":
		return "HTML"
	case ".css":
		return "CSS"
	case ".scss":
		return "SCSS"
	case ".sql":
		return "SQL"
	case ".md":
		return "Markdown"
	case ".proto":
		return "Protocol Buffers"
	case ".lua":
		return "Lua"
	case ".zig":
		return "Zig"
	case ".ex", ".exs":
		return "Elixir"
	case ".erl":
		return "Erlang"
	case ".hs":
		return "Haskell"
	case ".ml", ".mli":
		return "OCaml"
	case ".r":
		return "R"
	case ".dart":
		return "Dart"
	case ".vue":
		return "Vue"
	case ".svelte":
		return "Svelte"
	default:
		return ""
	}
}

// deduplicateSymbols removes duplicate symbols keyed by (Name, FilePath, Line).
func deduplicateSymbols(symbols []Symbol) []Symbol {
	type key struct {
		Name     string
		FilePath string
		Line     int
	}
	seen := make(map[key]struct{})
	var out []Symbol
	for _, s := range symbols {
		k := key{s.Name, s.FilePath, s.Line}
		if _, exists := seen[k]; exists {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	return out
}
