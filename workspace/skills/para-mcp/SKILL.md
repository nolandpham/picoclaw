---
name: para-mcp
description: Manage PARA library documents (list, add, update, delete) with independent data directory.
metadata: {"nanobot":{"emoji":"📚"}}
---

# PARA MCP Skill

Use this skill when user wants to list, create, update, or delete PARA documents.

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

- List documents: `list` or `list 01.projects`
- Show status: `status`
- Show stats: `stats`
- Add document:
  - Short content: `add <category> "<name>" --content="..." --tags="a,b"`
  - Long content (from web fetch, file, etc.): **2-step pattern** (see below)
- Update:
  - `update <id> --name="..." --content="..." --tags="a,b"`
- Delete:
  - `delete <id> --force`
- Link entities:
  - `link <from-id> <to-id>` / `unlink <from-id> <to-id>`

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
