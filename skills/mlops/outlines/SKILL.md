---
name: outlines
description: "Constrained text generation: regex, JSON schema, and Pydantic model-guided output."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [structured-output, json, regex, grammar, pydantic, constrained-generation]
  related_skills: [instructor, guidance, dspy]
---

# Outlines — Guaranteed Structured Generation

Generate text that is guaranteed to match a regex, JSON schema, or
Pydantic model — at the token level, not post-hoc validation.

## Installation

```bash
pip install outlines
```

## When to Use

- "generate valid JSON guaranteed"
- "force the output to match this regex"
- "generate text following this grammar"
- "produce output that validates against a Pydantic model"
- When you cannot tolerate ANY parse failures

## Key Difference from Instructor

| Feature | Outlines | Instructor |
|---------|----------|------------|
| Guarantee | Token-level (100%) | Retry-based (≈100%) |
| Speed | Slightly slower | Faster |
| Models | Open-source + API | OpenAI API focused |
| Complexity | More setup | Simpler API |

## Basic Patterns

### Regex Constraint
```python
import outlines

model = outlines.models.transformers("mistralai/Mistral-7B-Instruct-v0.2")

# Generate valid email
generator = outlines.generate.regex(model, r"[a-z0-9._]+@[a-z0-9]+\.[a-z]{2,}")
result = generator("Generate an email address:")
print(result)  # Guaranteed to match the regex

# Phone number
generator = outlines.generate.regex(model, r"\d{3}-\d{3}-\d{4}")
result = generator("Generate a phone number:")

# Enum choice
generator = outlines.generate.choice(model, ["red", "green", "blue", "yellow"])
```

### JSON Schema
```python
schema = """
{
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "age": {"type": "integer", "minimum": 0},
    "emails": {
      "type": "array",
      "items": {"type": "string", "format": "email"}
    }
  },
  "required": ["name", "age"]
}
"""
generator = outlines.generate.json(model, schema)
result = generator("Generate a user profile:")
```

### Pydantic Model
```python
from pydantic import BaseModel, Field

class Character(BaseModel):
    name: str = Field(description="Character name")
    role: str = Field(description="warrior, mage, or rogue")
    level: int = Field(ge=1, le=100)
    items: list[str]

generator = outlines.generate.json(model, Character)
result = generator("Create a fantasy RPG character:")
```

### Grammar (EBNF)
```python
arithmetic_grammar = """
?start: expression
?expression: term (("+" | "-") term)*
?term: factor (("*" | "/") factor)*
?factor: NUMBER | "(" expression ")"
NUMBER: /[0-9]+/
"""
generator = outlines.generate.cfg(model, arithmetic_grammar)
result = generator("Calculate: ")
```

## Model Options

```python
# HuggingFace transformers
model = outlines.models.transformers("microsoft/phi-2")

# llama.cpp GGUF
model = outlines.models.llamacpp("./models/model.Q4_K_M.gguf")

# OpenAI API (no guaranteed constraints, uses regex-guided sampling)
model = outlines.models.openai("gpt-4")

# vLLM
model = outlines.models.vllm("meta-llama/Llama-3-8B")
```
