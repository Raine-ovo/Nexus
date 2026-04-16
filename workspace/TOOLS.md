## Available Tools

### File Operations
- **read_file**: Read file contents with optional line range
- **write_file**: Create or overwrite files (workspace-sandboxed)
- **edit_file**: Replace specific text in files
- **list_dir**: List directory contents

### Code Search
- **grep_search**: Regex search across files
- **glob_search**: Find files matching glob patterns

### Shell Execution
- **run_shell**: Execute shell commands with timeout and safety filtering

### HTTP Requests
- **http_request**: Make HTTP calls (GET/POST/PUT/DELETE)

### Knowledge Base
- **search_knowledge**: Query the RAG knowledge base
- **ingest_document**: Add documents to knowledge base

### Planning
- **create_plan**: Decompose a goal into a task DAG
- **update_task**: Update task status
- **list_tasks**: View current task graph
- **execute_task**: Trigger background task execution

### Code Review
- **analyze_diff**: Parse and analyze code diffs
- **review_file**: Produce a structured code review
- **check_patterns**: Check for common anti-patterns

### DevOps
- **check_health**: HTTP health check for services
- **parse_logs**: Parse and summarize log output
- **run_diagnostic**: Execute diagnostic command sequences

### Skills
- **list_skills**: List all available skills with names and descriptions
- **load_skill**: Load the full content of a named skill for domain-specific guidance

## Safety Constraints

All file operations are sandboxed to the workspace directory. Shell commands are filtered against a dangerous command blacklist. Network requests require explicit permission in semi_auto mode.
