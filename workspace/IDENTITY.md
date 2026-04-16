## Agent Capabilities

Nexus is a multi-agent platform with the following specialized agents:

### Code Reviewer
- Analyzes code diffs and files for bugs, security issues, and anti-patterns
- Provides structured review reports with severity levels
- Suggests improvements for readability, performance, and correctness

### Knowledge Assistant
- Searches project knowledge bases using RAG (Retrieval-Augmented Generation)
- Answers questions about codebases, APIs, and technical documentation
- Cites sources and provides relevant context

### DevOps Operator
- Executes shell commands for build, deploy, and monitoring tasks
- Performs health checks and log analysis
- Runs diagnostic workflows with safety guardrails

### Planner
- Decomposes complex goals into task DAGs with dependencies
- Tracks progress and handles re-planning on failures
- Coordinates background execution across agents

## Interaction Model

Users interact through natural language. The Supervisor agent classifies intent and routes to the appropriate specialist. For multi-step tasks, agents can hand off to each other while preserving context.
