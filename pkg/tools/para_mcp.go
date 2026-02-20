package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	paraMCPSourceEnv = "PICOCLAW_PARA_MCP_DIR"
	paraMCPDataEnv   = "PICOCLAW_PARA_DATA_DIR"
)

type paraMCPDocument struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Category     string    `json:"category"`
	Content      string    `json:"content"`
	ContentHash  string    `json:"content_hash"`
	Tags         []string  `json:"tags"`
	LinkedIDs    []string  `json:"linked_ids"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type paraMCPConfig struct {
	DataDir     string `json:"data_dir"`
	Initialized bool   `json:"initialized"`
	Version     string `json:"version"`
}

type paraMCPStats struct {
	Total      int            `json:"total"`
	ByCategory map[string]int `json:"by_category"`
	Links      int            `json:"links"`
}

type paraMCPStatus struct {
	Initialized bool         `json:"initialized"`
	DataDir     string       `json:"data_dir"`
	Version     string       `json:"version"`
	Stats       paraMCPStats `json:"stats"`
}

type indexedDocument struct {
	Index     int       `json:"index"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type paraMCPToolBase struct {
	sourceDir string
	dataDir   string
}

func newParaMCPToolBase(workspace string) paraMCPToolBase {
	sourceDir := os.Getenv(paraMCPSourceEnv)
	if strings.TrimSpace(sourceDir) == "" {
		sourceDir = filepath.Join(workspace, "para-mcp")
	}

	dataDir := os.Getenv(paraMCPDataEnv)
	if strings.TrimSpace(dataDir) == "" {
		dataDir = filepath.Join(workspace, "para-data")
	}

	return paraMCPToolBase{
		sourceDir: sourceDir,
		dataDir:   dataDir,
	}
}

func (b paraMCPToolBase) paraArgs(extra ...string) []string {
	base := []string{"run", ".", "--data-dir", b.dataDir}
	return append(base, extra...)
}

func (b paraMCPToolBase) runPara(ctx context.Context, extra ...string) *ToolResult {
	if _, err := os.Stat(b.sourceDir); err != nil {
		return ErrorResult(fmt.Sprintf("para-mcp source not found: %s", b.sourceDir)).WithError(err)
	}

	if err := os.MkdirAll(b.dataDir, 0o755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to prepare para data dir: %v", err)).WithError(err)
	}

	cmd := exec.CommandContext(ctx, "go", b.paraArgs(extra...)...)
	cmd.Dir = b.sourceDir
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if text == "" {
		text = "ok"
	}

	if err != nil {
		return ErrorResult(text).WithError(err)
	}

	return UserResult(text)
}

func (b paraMCPToolBase) documentsPath() string {
	return filepath.Join(b.dataDir, "documents.json")
}

func (b paraMCPToolBase) configPath() string {
	return filepath.Join(b.dataDir, "config.json")
}

func (b paraMCPToolBase) loadDocuments() (map[string]*paraMCPDocument, error) {
	path := b.documentsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*paraMCPDocument{}, nil
		}
		return nil, err
	}

	docs := map[string]*paraMCPDocument{}
	if err := json.Unmarshal(data, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (b paraMCPToolBase) loadConfig() (*paraMCPConfig, error) {
	path := b.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &paraMCPConfig{DataDir: b.dataDir, Initialized: false, Version: "0.1.0"}, nil
		}
		return nil, err
	}

	var cfg paraMCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.DataDir == "" {
		cfg.DataDir = b.dataDir
	}
	if cfg.Version == "" {
		cfg.Version = "0.1.0"
	}
	return &cfg, nil
}

func encodePrettyJSON(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func toCommaString(value interface{}) string {
	switch tags := value.(type) {
	case string:
		return tags
	case []string:
		return strings.Join(tags, ",")
	case []interface{}:
		parts := make([]string, 0, len(tags))
		for _, item := range tags {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

// computeContentHash generates a SHA256 hash of document content for duplicate detection
func computeContentHash(name, category, content string) string {
	combined := fmt.Sprintf("%s::%s::%s", category, name, content)
	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:])
}

// findDuplicateByHash searches for documents with the same content hash, computing hashes on the fly
func findDuplicateByHash(docsMap map[string]*paraMCPDocument, targetHash string, targetName, targetCategory, targetContent string) *paraMCPDocument {
	for _, doc := range docsMap {
		// If document has stored hash, use it
		if doc.ContentHash != "" {
			if doc.ContentHash == targetHash {
				return doc
			}
		} else {
			// Compute hash on the fly for documents without stored hash
			docHash := computeContentHash(doc.Name, doc.Category, doc.Content)
			if docHash == targetHash {
				return doc
			}
		}
	}
	return nil
}

// findSimilarDocuments finds documents with similar names or content (for suggestions)
func findSimilarDocuments(docsMap map[string]*paraMCPDocument, name, category string) []*paraMCPDocument {
	var similar []*paraMCPDocument
	nameLower := strings.ToLower(name)
	
	for _, doc := range docsMap {
		if doc.Category != category {
			continue
		}
		docNameLower := strings.ToLower(doc.Name)
		// Check if names are very similar (70%+ match on keywords)
		if stringSimilarity(nameLower, docNameLower) > 0.7 {
			similar = append(similar, doc)
		}
	}
	
	return similar
}

// stringSimilarity calculates Levenshtein-like similarity (simple version)
func stringSimilarity(s1, s2 string) float64 {
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}
	if maxLen == 0 {
		return 1.0
	}
	
	distance := levenshteinDistance(s1, s2)
	return 1.0 - (float64(distance) / float64(maxLen))
}

// levenshteinDistance calculates the Levenshtein distance between two strings
func levenshteinDistance(s1, s2 string) int {
	len1 := len(s1)
	len2 := len(s2)
	
	if len1 == 0 {
		return len2
	}
	if len2 == 0 {
		return len1
	}
	
	// Create distance matrix
	d := make([][]int, len1+1)
	for i := range d {
		d[i] = make([]int, len2+1)
	}
	
	for i := 0; i <= len1; i++ {
		d[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		d[0][j] = j
	}
	
	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}
			d[i][j] = min(d[i-1][j]+1, min(d[i][j-1]+1, d[i-1][j-1]+cost))
		}
	}
	
	return d[len1][len2]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func NewParaMCPTools(workspace string) []Tool {
	base := newParaMCPToolBase(workspace)
	return []Tool{
		&listDocumentsTool{base: base},
		&getDocumentTool{base: base},
		&addDocumentTool{base: base},
		&updateDocumentTool{base: base},
		&deleteDocumentTool{base: base},
		&linkEntitiesTool{base: base},
		&unlinkEntitiesTool{base: base},
		&getStatsTool{base: base},
		&getStatusTool{base: base},
		&checkDuplicatesTool{base: base},
	}
}

type listDocumentsTool struct{ base paraMCPToolBase }

func (t *listDocumentsTool) Name() string { return "list_documents" }

func (t *listDocumentsTool) Description() string {
	return "List PARA documents, optionally filtered by category (01.projects, 02.areas, 03.resources, 04.archives)."
}

func (t *listDocumentsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"category": map[string]interface{}{
				"type":        "string",
				"description": "PARA category filter: 01.projects, 02.areas, 03.resources, 04.archives",
			},
		},
	}
}

func (t *listDocumentsTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("list documents: %v", err)).WithError(err)
	}

	category, _ := args["category"].(string)
	category = strings.TrimSpace(category)

	docs := make([]*paraMCPDocument, 0, len(docsMap))
	for _, doc := range docsMap {
		if category == "" || doc.Category == category {
			docs = append(docs, doc)
		}
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})

	// Build indexed list
	indexed := make([]indexedDocument, len(docs))
	for i, doc := range docs {
		indexed[i] = indexedDocument{
			Index:     i + 1,
			ID:        doc.ID,
			Name:      doc.Name,
			Category:  doc.Category,
			Tags:      doc.Tags,
			CreatedAt: doc.CreatedAt,
			UpdatedAt: doc.UpdatedAt,
		}
	}

	// Build formatted text output
	var sb strings.Builder
	if len(indexed) == 0 {
		sb.WriteString("No documents found")
	} else {
		sb.WriteString(fmt.Sprintf("Found %d document(s):\n\n", len(indexed)))
		for _, idx := range indexed {
			sb.WriteString(fmt.Sprintf("[%02d] %s (%s)\n", idx.Index, idx.Name, idx.Category))
			if len(idx.Tags) > 0 {
				sb.WriteString(fmt.Sprintf("      Tags: %s\n", strings.Join(idx.Tags, ", ")))
			}
			sb.WriteString(fmt.Sprintf("      ID: %s\n", idx.ID))
			sb.WriteString(fmt.Sprintf("      Updated: %s\n\n", idx.UpdatedAt.Format("2006-01-02 15:04")))
		}
	}

	// Also include JSON data for structured access
	jsonData, err := encodePrettyJSON(indexed)
	if err != nil {
		return ErrorResult(fmt.Sprintf("list documents: %v", err)).WithError(err)
	}

	output := fmt.Sprintf("%s\n--- JSON DATA ---\n%s", sb.String(), jsonData)
	return UserResult(output)
}

type getDocumentTool struct{ base paraMCPToolBase }

func (t *getDocumentTool) Name() string { return "get_document" }

func (t *getDocumentTool) Description() string {
	return "Get a PARA document by ID or index number."
}

func (t *getDocumentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Document ID",
			},
			"index": map[string]interface{}{
				"type":        "integer",
				"description": "Document index number (01, 02, 03, ...) from list_documents output",
			},
		},
	}
}

func (t *getDocumentTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)

	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("get document: %v", err)).WithError(err)
	}

	var doc *paraMCPDocument

	// Try ID lookup first
	if id != "" {
		if d, ok := docsMap[id]; ok {
			doc = d
		} else {
			return ErrorResult(fmt.Sprintf("get document: document %s not found", id))
		}
	} else if idx, ok := args["index"].(float64); ok {
		index := int(idx)
		if index <= 0 {
			return ErrorResult("index must be a positive number")
		}

		// Get all documents sorted by update time
		docs := make([]*paraMCPDocument, 0, len(docsMap))
		for _, d := range docsMap {
			docs = append(docs, d)
		}
		sort.Slice(docs, func(i, j int) bool {
			return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
		})

		if index > len(docs) {
			return ErrorResult(fmt.Sprintf("index %d out of range (total: %d)", index, len(docs)))
		}

		doc = docs[index-1]
	} else {
		return ErrorResult("either 'id' or 'index' parameter is required")
	}

	text, err := encodePrettyJSON(doc)
	if err != nil {
		return ErrorResult(fmt.Sprintf("get document: %v", err)).WithError(err)
	}

	return UserResult(text)
}

type addDocumentTool struct{ base paraMCPToolBase }

func (t *addDocumentTool) Name() string { return "add_document" }

func (t *addDocumentTool) Description() string { return "Add a new PARA document." }

func (t *addDocumentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Document name",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "PARA category: 01.projects, 02.areas, 03.resources, 04.archives",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Document content",
			},
			"tags": map[string]interface{}{
				"type":        "string",
				"description": "Comma-separated list of tags",
			},
		},
		"required": []string{"name", "category"},
	}
}

func (t *addDocumentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	category, _ := args["category"].(string)
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)

	if name == "" {
		return ErrorResult("name is required")
	}
	if category == "" {
		return ErrorResult("category is required")
	}

	content := ""
	if c, ok := args["content"]; ok {
		content = fmt.Sprintf("%v", c)
	}

	// Check for duplicates before adding
	docsMap, err := t.base.loadDocuments()
	if err == nil {
		contentHash := computeContentHash(name, category, content)
		
		// Check for exact duplicate (same hash)
		if dup := findDuplicateByHash(docsMap, contentHash, name, category, content); dup != nil {
			return ErrorResult(fmt.Sprintf("DUPLICATE DETECTED: Document with identical content already exists:\n  ID: %s\n  Name: %s\n  Category: %s\n  Created: %s\n\nUse update_document to modify existing document instead.", dup.ID, dup.Name, dup.Category, dup.CreatedAt.Format("2006-01-02 15:04")))
		}
		
		// Check for similar documents (same category, similar name)
		similar := findSimilarDocuments(docsMap, name, category)
		if len(similar) > 0 {
			var suggestion strings.Builder
			suggestion.WriteString(fmt.Sprintf("WARNING: Found %d similar document(s) in %s category:\n", len(similar), category))
			for i, sim := range similar {
				if i < 3 { // Show max 3 suggestions
					suggestion.WriteString(fmt.Sprintf("  [%d] %s (ID: %s, Updated: %s)\n", i+1, sim.Name, sim.ID, sim.UpdatedAt.Format("2006-01-02 15:04")))
				}
			}
			suggestion.WriteString("\nConsider if you should update existing document instead. Proceeding with add...\n")
		}
	}

	cmdArgs := []string{"add", category, name}
	if content != "" {
		cmdArgs = append(cmdArgs, "--content", content)
	}
	if tags, ok := args["tags"]; ok {
		cmdArgs = append(cmdArgs, "--tags", toCommaString(tags))
	}

	return t.base.runPara(ctx, cmdArgs...)
}

type updateDocumentTool struct{ base paraMCPToolBase }

func (t *updateDocumentTool) Name() string { return "update_document" }

func (t *updateDocumentTool) Description() string { return "Update an existing PARA document." }

func (t *updateDocumentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Document ID",
			},
			"index": map[string]interface{}{
				"type":        "integer",
				"description": "Document index number from list_documents output",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "New document name",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "New PARA category",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "New document content",
			},
			"tags": map[string]interface{}{
				"type":        "string",
				"description": "New comma-separated list of tags",
			},
		},
	}
}

func (t *updateDocumentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)

	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("update document: %v", err)).WithError(err)
	}

	// Try ID lookup first
	if id != "" {
		if _, ok := docsMap[id]; !ok {
			return ErrorResult(fmt.Sprintf("update document: document %s not found", id))
		}
	} else if idx, ok := args["index"].(float64); ok {
		index := int(idx)
		docs := make([]*paraMCPDocument, 0, len(docsMap))
		for _, d := range docsMap {
			docs = append(docs, d)
		}
		sort.Slice(docs, func(i, j int) bool {
			return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
		})

		if index <= 0 || index > len(docs) {
			return ErrorResult(fmt.Sprintf("index %d out of range (total: %d)", index, len(docs)))
		}

		id = docs[index-1].ID
	} else {
		return ErrorResult("either 'id' or 'index' parameter is required")
	}

	cmdArgs := []string{"update", id}
	if name, ok := args["name"]; ok {
		cmdArgs = append(cmdArgs, "--name", fmt.Sprintf("%v", name))
	}
	if category, ok := args["category"]; ok {
		cmdArgs = append(cmdArgs, "--category", fmt.Sprintf("%v", category))
	}
	if content, ok := args["content"]; ok {
		cmdArgs = append(cmdArgs, "--content", fmt.Sprintf("%v", content))
	}
	if tags, ok := args["tags"]; ok {
		cmdArgs = append(cmdArgs, "--tags", toCommaString(tags))
	}

	if len(cmdArgs) == 2 {
		return ErrorResult("at least one field to update is required")
	}

	return t.base.runPara(ctx, cmdArgs...)
}

type deleteDocumentTool struct{ base paraMCPToolBase }

func (t *deleteDocumentTool) Name() string { return "delete_document" }

func (t *deleteDocumentTool) Description() string {
	return "Delete a PARA document by ID or index."
}

func (t *deleteDocumentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Document ID",
			},
			"index": map[string]interface{}{
				"type":        "integer",
				"description": "Document index number from list_documents output",
			},
		},
	}
}

func (t *deleteDocumentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)

	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("delete document: %v", err)).WithError(err)
	}

	// Try ID lookup first
	if id != "" {
		if _, ok := docsMap[id]; !ok {
			return ErrorResult(fmt.Sprintf("delete document: document %s not found", id))
		}
	} else if idx, ok := args["index"].(float64); ok {
		index := int(idx)
		docs := make([]*paraMCPDocument, 0, len(docsMap))
		for _, d := range docsMap {
			docs = append(docs, d)
		}
		sort.Slice(docs, func(i, j int) bool {
			return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
		})

		if index <= 0 || index > len(docs) {
			return ErrorResult(fmt.Sprintf("index %d out of range (total: %d)", index, len(docs)))
		}

		id = docs[index-1].ID
	} else {
		return ErrorResult("either 'id' or 'index' parameter is required")
	}

	return t.base.runPara(ctx, "delete", id, "--force")
}

type linkEntitiesTool struct{ base paraMCPToolBase }

func (t *linkEntitiesTool) Name() string { return "link_entities" }

func (t *linkEntitiesTool) Description() string {
	return "Create a bidirectional link between two PARA documents."
}

func (t *linkEntitiesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"from_id": map[string]interface{}{
				"type":        "string",
				"description": "Source document ID",
			},
			"from_index": map[string]interface{}{
				"type":        "integer",
				"description": "Source document index from list_documents",
			},
			"to_id": map[string]interface{}{
				"type":        "string",
				"description": "Target document ID",
			},
			"to_index": map[string]interface{}{
				"type":        "integer",
				"description": "Target document index from list_documents",
			},
		},
	}
}

func (t *linkEntitiesTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("link entities: %v", err)).WithError(err)
	}

	docs := make([]*paraMCPDocument, 0, len(docsMap))
	for _, d := range docsMap {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})

	// Resolve from_id
	fromID, _ := args["from_id"].(string)
	fromID = strings.TrimSpace(fromID)
	if fromID == "" {
		if fromIdx, ok := args["from_index"].(float64); ok {
			idx := int(fromIdx)
			if idx <= 0 || idx > len(docs) {
				return ErrorResult(fmt.Sprintf("from_index %d out of range", idx))
			}
			fromID = docs[idx-1].ID
		} else {
			return ErrorResult("either 'from_id' or 'from_index' is required")
		}
	}

	// Resolve to_id
	toID, _ := args["to_id"].(string)
	toID = strings.TrimSpace(toID)
	if toID == "" {
		if toIdx, ok := args["to_index"].(float64); ok {
			idx := int(toIdx)
			if idx <= 0 || idx > len(docs) {
				return ErrorResult(fmt.Sprintf("to_index %d out of range", idx))
			}
			toID = docs[idx-1].ID
		} else {
			return ErrorResult("either 'to_id' or 'to_index' is required")
		}
	}

	return t.base.runPara(ctx, "link", fromID, toID)
}

type unlinkEntitiesTool struct{ base paraMCPToolBase }

func (t *unlinkEntitiesTool) Name() string { return "unlink_entities" }

func (t *unlinkEntitiesTool) Description() string {
	return "Remove a bidirectional link between two PARA documents."
}

func (t *unlinkEntitiesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"from_id": map[string]interface{}{
				"type":        "string",
				"description": "Source document ID",
			},
			"from_index": map[string]interface{}{
				"type":        "integer",
				"description": "Source document index from list_documents",
			},
			"to_id": map[string]interface{}{
				"type":        "string",
				"description": "Target document ID",
			},
			"to_index": map[string]interface{}{
				"type":        "integer",
				"description": "Target document index from list_documents",
			},
		},
	}
}

func (t *unlinkEntitiesTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("unlink entities: %v", err)).WithError(err)
	}

	docs := make([]*paraMCPDocument, 0, len(docsMap))
	for _, d := range docsMap {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})

	// Resolve from_id
	fromID, _ := args["from_id"].(string)
	fromID = strings.TrimSpace(fromID)
	if fromID == "" {
		if fromIdx, ok := args["from_index"].(float64); ok {
			idx := int(fromIdx)
			if idx <= 0 || idx > len(docs) {
				return ErrorResult(fmt.Sprintf("from_index %d out of range", idx))
			}
			fromID = docs[idx-1].ID
		} else {
			return ErrorResult("either 'from_id' or 'from_index' is required")
		}
	}

	// Resolve to_id
	toID, _ := args["to_id"].(string)
	toID = strings.TrimSpace(toID)
	if toID == "" {
		if toIdx, ok := args["to_index"].(float64); ok {
			idx := int(toIdx)
			if idx <= 0 || idx > len(docs) {
				return ErrorResult(fmt.Sprintf("to_index %d out of range", idx))
			}
			toID = docs[idx-1].ID
		} else {
			return ErrorResult("either 'to_id' or 'to_index' is required")
		}
	}

	return t.base.runPara(ctx, "unlink", fromID, toID)
}

type getStatsTool struct{ base paraMCPToolBase }

func (t *getStatsTool) Name() string { return "get_stats" }

func (t *getStatsTool) Description() string {
	return "Get aggregate statistics for all PARA documents."
}

func (t *getStatsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *getStatsTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("get stats: %v", err)).WithError(err)
	}

	stats := paraMCPStats{
		Total: len(docsMap),
		ByCategory: map[string]int{
			"01.projects":  0,
			"02.areas":     0,
			"03.resources": 0,
			"04.archives":  0,
		},
	}

	totalLinks := 0
	for _, doc := range docsMap {
		stats.ByCategory[doc.Category]++
		totalLinks += len(doc.LinkedIDs)
	}
	stats.Links = totalLinks / 2

	text, err := encodePrettyJSON(stats)
	if err != nil {
		return ErrorResult(fmt.Sprintf("get stats: %v", err)).WithError(err)
	}

	return UserResult(text)
}

type getStatusTool struct{ base paraMCPToolBase }

func (t *getStatusTool) Name() string { return "get_status" }

func (t *getStatusTool) Description() string { return "Get the current status of the PARA librarian." }

func (t *getStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *getStatusTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	cfg, err := t.base.loadConfig()
	if err != nil {
		return ErrorResult(fmt.Sprintf("get status: %v", err)).WithError(err)
	}

	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("get status: %v", err)).WithError(err)
	}

	stats := paraMCPStats{
		Total: len(docsMap),
		ByCategory: map[string]int{
			"01.projects":  0,
			"02.areas":     0,
			"03.resources": 0,
			"04.archives":  0,
		},
	}

	totalLinks := 0
	for _, doc := range docsMap {
		stats.ByCategory[doc.Category]++
		totalLinks += len(doc.LinkedIDs)
	}
	stats.Links = totalLinks / 2

	status := paraMCPStatus{
		Initialized: cfg.Initialized,
		DataDir:     cfg.DataDir,
		Version:     cfg.Version,
		Stats:       stats,
	}

	text, err := encodePrettyJSON(status)
	if err != nil {
		return ErrorResult(fmt.Sprintf("get status: %v", err)).WithError(err)
	}

	return UserResult(text)
}
type checkDuplicatesTool struct{ base paraMCPToolBase }

func (t *checkDuplicatesTool) Name() string { return "check_duplicates" }

func (t *checkDuplicatesTool) Description() string {
	return "Check for duplicate or similar documents in PARA by name, category, or content hash."
}

func (t *checkDuplicatesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Document name to check for duplicates",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "PARA category to filter search (optional)",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Document content to check for exact duplicates (optional)",
			},
			"show_all_similar": map[string]interface{}{
				"type":        "boolean",
				"description": "Show all similar documents instead of just top 3 (default: false)",
			},
		},
		"required": []string{"name"},
	}
}

type duplicateCheckResult struct {
	Name              string                 `json:"name"`
	Category          string                 `json:"category,omitempty"`
	ExactDuplicate    *paraMCPDocument       `json:"exact_duplicate,omitempty"`
	SimilarDocuments  []*paraMCPDocument     `json:"similar_documents,omitempty"`
	ContentHash       string                 `json:"content_hash,omitempty"`
	Message           string                 `json:"message"`
}

func (t *checkDuplicatesTool) Execute(_ context.Context, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrorResult("name is required")
	}

	category, _ := args["category"].(string)
	category = strings.TrimSpace(category)

	content := ""
	if c, ok := args["content"]; ok {
		content = fmt.Sprintf("%v", c)
	}

	showAll := false
	if sa, ok := args["show_all_similar"].(bool); ok {
		showAll = sa
	}

	docsMap, err := t.base.loadDocuments()
	if err != nil {
		return ErrorResult(fmt.Sprintf("check duplicates: %v", err)).WithError(err)
	}

	result := duplicateCheckResult{
		Name:     name,
		Category: category,
	}

	// Check for exact content duplicate
	if content != "" {
		contentHash := computeContentHash(name, category, content)
		result.ContentHash = contentHash

		if dup := findDuplicateByHash(docsMap, contentHash, name, category, content); dup != nil {
			result.ExactDuplicate = dup
			result.Message = fmt.Sprintf("EXACT DUPLICATE FOUND: Document '%s' (ID: %s) has identical content", dup.Name, dup.ID)
		}
	}

	// Check for similar documents
	var similar []*paraMCPDocument
	if category != "" {
		similar = findSimilarDocuments(docsMap, name, category)
	} else {
		// Search in all categories
		tmpMap := make(map[string]*paraMCPDocument)
		docs := make([]*paraMCPDocument, 0, len(docsMap))
		for _, doc := range docsMap {
			tmpMap[doc.ID] = doc
			docs = append(docs, doc)
		}

		nameLower := strings.ToLower(name)
		for _, doc := range docs {
			docNameLower := strings.ToLower(doc.Name)
			if stringSimilarity(nameLower, docNameLower) > 0.7 {
				similar = append(similar, doc)
			}
		}
	}

	// Limit similar results unless show_all_similar is true
	if len(similar) > 0 && !showAll && len(similar) > 3 {
		similar = similar[:3]
		result.Message += fmt.Sprintf(" (%d more similar documents exist, use show_all_similar=true to see all)", len(similar)-3)
	}

	result.SimilarDocuments = similar

	if result.ExactDuplicate == nil && len(result.SimilarDocuments) == 0 {
		result.Message = "No duplicates or similar documents found."
	} else if result.ExactDuplicate == nil && len(result.SimilarDocuments) > 0 {
		result.Message = fmt.Sprintf("Found %d similar document(s), but no exact duplicates.", len(result.SimilarDocuments))
	}

	text, err := encodePrettyJSON(result)
	if err != nil {
		return ErrorResult(fmt.Sprintf("check duplicates: %v", err)).WithError(err)
	}

	return UserResult(text)
}