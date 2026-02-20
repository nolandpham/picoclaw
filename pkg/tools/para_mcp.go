package tools

import (
	"context"
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
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	LinkedIDs []string  `json:"linked_ids"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

	cmdArgs := []string{"add", category, name}
	if content, ok := args["content"]; ok {
		cmdArgs = append(cmdArgs, "--content", fmt.Sprintf("%v", content))
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
