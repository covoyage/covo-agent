---
name: guidance
description: "Microsoft Guidance: constrained text generation with regex, grammars, and structured programs."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [structured-output, prompt-engineering, microsoft, guidance, grammar]
  related_skills: [outlines, instructor, dspy]
---

# Guidance — Microsoft Constrained Generation

Build structured LLM programs with precise output control. Guidance uses
a template language that mixes natural language with code for guaranteed
format compliance.

## Installation

```bash
pip install guidance
```

## When to Use

- "generate structured output with a template"
- "build a multi-step LLM program"
- "force specific output format"
- "combine prompting with programmatic control"

## Key Features

- **Handlebars templates** with Python expressions
- **Token-level constraints** (regex, select, JSON)
- **Multi-step programs** with state and branching
- **Tool integration** within templates

## Basic Examples

### Simple Generation
```python
import guidance

lm = guidance.llms.OpenAI("gpt-5.6")
guidance.llm = lm

program = guidance('''
Answer the following question:
{{question}}

Answer: {{gen "answer" max_tokens=100}}
''')
result = program(question="What is quantum computing?")
print(result["answer"])
```

### Structured Output with Select
```python
program = guidance('''
Classify the sentiment of this review:
{{review}}

Sentiment: {{select "sentiment" options=["positive", "negative", "neutral"]}}
Confidence: {{select "confidence" options=["high", "medium", "low"]}}
Reason: {{gen "reason" max_tokens=50}}
''')
result = program(review="The product arrived damaged.")
print(result["sentiment"])  # Guaranteed one of the options
```

### JSON Output
```python
program = guidance('''
Extract the person from this text as JSON:
{{text}}

```json
{
  "name": "{{gen 'name' max_tokens=20}}",
  "age": {{gen 'age' max_tokens=3 pattern='[0-9]+'}},
  "occupation": "{{gen 'occupation' max_tokens=20}}"
}
```
''')
result = program(text="John, 34, works as an engineer")
```

### Multi-step with Branching
```python
program = guidance('''
Analyze this code:

{{code}}

Classification: {{select "type" options=["bug", "feature", "refactor", "docs"]}}

{{#if (eq type "bug")}}
Severity: {{select "severity" options=["critical", "major", "minor"]}}
Fix suggestion: {{gen "fix" max_tokens=200}}
{{else if (eq type "feature")}}
Complexity: {{select "complexity" options=["small", "medium", "large"]}}
Implementation plan: {{gen "plan" max_tokens=200}}
{{/if}}
''')
```

### Role-Based Chat
```python
program = guidance('''
{{#system}}You are a helpful coding assistant.{{/system}}
{{#user}}Write a Python function to reverse a linked list.{{/user}}
{{#assistant}}{{gen "code" max_tokens=300}}{{/assistant}}
''')
```

### Tool Calling Pattern
```python
tools = ["search", "execute_code", "read_file"]

program = guidance('''
{{#system}}
You can use these tools: {{tools}}
To use a tool, respond with: TOOL: <name>
{{/system}}
{{#user}}{{question}}{{/user}}
{{#assistant}}
{{gen "response" max_tokens=200}}
{{/assistant}}
{{#if (contains response "TOOL:")}}
Tool result: {{tool_result}}
{{geneach "followup" num_iterations=3}}
  {{gen "this.response" max_tokens=200}}
{{/geneach}}
{{/if}}
''')
```

## When to Prefer Guidance Over Others

| Use Case | Best Tool |
|----------|-----------|
| OpenAI API only, simple extraction | Instructor |
| Token-level guarantee, open-source models | Outlines |
| **Multi-step templated programs with logic** | **Guidance** |
| Auto-optimize prompts and pipelines | DSPy |
