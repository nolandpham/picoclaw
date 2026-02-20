---
name: para-mcp
description: Manage PARA library documents (list, add, update, delete) with independent data directory.
metadata: {"nanobot":{"emoji":"📚"}}
---

# PARA MCP Skill

Use this skill when user wants to list, create, update, or delete PARA documents.

## Intent Triggers (Vietnamese)

When user says phrases like:

- `thêm ý tưởng vào project para-mcp`
- `lưu ý tưởng này vào para`
- `ghi lại ý tưởng này`
- `thêm vào thư viện para`

Treat this as a **write request**, not a brainstorming request.

You MUST call PARA tools directly (prefer `add_document`) instead of asking follow-up confirmation questions.

Default mapping for idea-capture prompts:

- `category`: `01.projects`
- `name`: `Ý tưởng: <short title from user prompt>`
- `content`: full user idea text
- `tags`: `idea,para-mcp`

After successful tool execution, return the created document ID.

## Paths

- para-mcp source (read-only): `/home/picoclaw/.picoclaw/workspace/para-mcp`
- PARA data directory: `/home/picoclaw/.picoclaw/workspace/para-data`
  - Maps directly to host `~/.para` (independent of Picoclaw).

## Standard Command

Always execute via `exec` tool:

```bash
cd /home/picoclaw/.picoclaw/workspace/para-mcp && go run . --data-dir /home/picoclaw/.picoclaw/workspace/para-data <subcommand>
```

## Operations

- List documents: `list_documents` or `list_documents category=01.projects`
- Show status: `get_status`
- Show stats: `get_stats`
- Get document: `get_document id=<ID>` or `get_document index=1` (from list_documents)
- Add document: `add_document name="..." category="01.projects" content="..." tags="..."`
  - **Automatic duplicate detection**: Prevents adding documents with identical name+content
  - If exact duplicate found: Tool rejects with error showing existing document ID
  - If similar documents exist: Tool shows warning suggesting to update instead
- Check duplicates: `check_duplicates name="..." category="01.projects" content="..." show_all_similar=true`
  - Scans for exact duplicates using SHA256 content hash
  - Finds similar documents using Levenshtein distance algorithm
  - Returns exact matches and suggestions for updates
- Update document: `update_document id=<ID> name="..." content="..."` or `update_document index=1 name="..."`
- Delete document: `delete_document id=<ID>` or `delete_document index=1`
- Link entities: `link_entities from_id=<ID> to_id=<ID>` or `link_entities from_index=1 to_index=2`
- Unlink entities: `unlink_entities from_id=<ID> to_id=<ID>` or `unlink_entities from_index=1 to_index=2`

## Index-Based Lookup

When you call `list_documents`, the output shows document numbers like `[01]`, `[02]`, etc. You can use these indices directly in other tools instead of copying IDs:

```
Found 5 document(s):

[01] Ý tưởng: YouTube transcript vào PARA (01.projects)
      Tags: idea, youtube, transcript
      ID: 3ca920c5-1c66-4a66-b5fe-103c83c87c70
      Updated: 2026-02-20 14:30

[02] Ý tưởng: PDF to MD và lưu vào PARA (01.projects)
      Tags: idea, pdf, markdown
      ID: f012baef-1078-4ffd-b2d7-e5d7cbae4193
      Updated: 2026-02-20 10:15
```

**Using indices in tool calls:**
- Get index 01: `get_document index=1`
- Get index 02: `get_document index=2`
- View details: `get_document index=1` returns full document JSON
- Delete index 01: `delete_document index=1` (be careful!)
- Update index 02: `update_document index=2 name="Updated Title"`
- Link documents: `link_entities from_index=1 to_index=2`
- Unlink documents: `unlink_entities from_index=1 to_index=2`

**Index numbers are based on update time (newest first)**, so index 01 is the most recently updated document.

You can also still use document IDs if you prefer: `get_document id=3ca920c5-1c66-4a66-b5fe-103c83c87c70`

## Duplicate Prevention System

**Problem solved**: Prevent accidental creation of duplicate ideas/documents

**How it works**:

1. **Exact Duplicate Detection**
   - Uses SHA256 hash of (category + name + content)
   - When adding a document, automatically checks all existing docs for content hash match
   - If found: Rejects add_document and suggests using update_document instead
   - Example: Trying to add "Ý tưởng: YouTube..." with identical content → ERROR with existing doc ID

2. **Similar Document Warnings**
   - Uses Levenshtein distance algorithm to find similar names
   - Calculates similarity > 70% as "similar"
   - Shows up to 3 suggestions when adding new document
   - Helps catch human-created duplicates ("YouTube transcript" vs "Youtube transcription")

3. **Proactive Duplicate Checking**
   - Tool: `check_duplicates name="..." category="01.projects" content="..." show_all_similar=true`
   - Before adding an idea, use this to verify it doesn't already exist
   - Can search by name alone, or name + content for exact match detection

**Example workflows**:

```
# Check if idea exists before adding
→ check_duplicates name="Ý tưởng: YouTube transcript..." category="01.projects"
Return:
  - No exact duplicates found
  - Found 2 similar documents (YouTube, youtube)

# Try to add exact duplicate
→ add_document name="Ý tưởng: PDF..." category="01.projects" content="..."
Return: ERROR - DUPLICATE DETECTED (existing ID: abc123...)

# Correct action: Update instead
→ update_document id=abc123 name="Updated name" content="new content"
```

## 2-Step Pattern for Long Content

**When**: User wants to save web articles, long documents, or content from fetch tool.

**How to execute**:
1. **Step 1** - Create temp file with content from web_fetch or other source:
   ```bash
   exec write_file /home/picoclaw/.picoclaw/workspace/para-data/article.txt
   # article content...
   ```
2. **Step 2** - Add document to PARA using --content-file:
   ```bash
   cd /home/picoclaw/.picoclaw/workspace/para-mcp && go run . --data-dir /home/picoclaw/.picoclaw/workspace/para-data add 03.resources "Article Title" --content-file=/home/picoclaw/.picoclaw/workspace/para-data/article.txt --tags=web,article
   ```

**Benefits**: Avoids `$(...)` command substitution (blocked by guard) and supports unlimited content length.

## Rules

- Valid categories: `01.projects`, `02.areas`, `03.resources`, `04.archives`.
- Always confirm with user before delete commands.
- For long content (web articles, documents): Use 2-step pattern (write file, then add with --content-file).
- Paths must be within workspace directory (guard blocks `/tmp`).
