---
name: instructor
description: "Structured LLM extraction with Pydantic validation, auto-retry on parse failures."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [structured-output, pydantic, extraction, validation, instructor, openai]
  related_skills: [dspy, outlines, guidance]
---

# Instructor — Pydantic Structured Output

Extract reliably typed data from LLM responses using Pydantic models.
Instructor validates and auto-retries on malformed output.

## Installation

```bash
pip install instructor
```

## When to Use

- "extract structured data from this text"
- "parse the LLM output into a typed object"
- "make the model return valid JSON every time"
- "classify text into categories with confidence scores"

## Basic Extraction

```python
import instructor
from openai import OpenAI
from pydantic import BaseModel, Field

client = instructor.from_openai(OpenAI())

class Person(BaseModel):
    name: str = Field(description="Full name")
    age: int = Field(ge=0, le=120)
    occupation: str
    skills: list[str]

# Extract from text
person = client.chat.completions.create(
    model="gpt-5.6",
    response_model=Person,
    messages=[{"role": "user", "content": 
        "John Smith is a 34-year-old software engineer skilled in Python, Rust, and Kubernetes."}],
)
print(person.name)  # John Smith
print(person.skills) # ['Python', 'Rust', 'Kubernetes']
```

## Advanced Patterns

### Nested Models
```python
class Address(BaseModel):
    street: str
    city: str
    country: str

class User(BaseModel):
    name: str
    address: Address
    emails: list[str]
```

### Classification with Reasoning
```python
class Sentiment(BaseModel):
    sentiment: str = Field(description="positive, negative, or neutral")
    confidence: float = Field(ge=0, le=1)
    reasoning: str = Field(description="why this classification was chosen")
```

### Iterable Extraction
```python
class Item(BaseModel):
    name: str
    price: float
    in_stock: bool

items = client.chat.completions.create(
    model="gpt-5.6",
    response_model=instructor.MultiTask(Item),
    messages=[{"role": "user", "content": catalog_text}],
)
for item in items:
    print(f"{item.name}: ${item.price}")
```

### Streaming Partial Results
```python
for partial in client.chat.completions.create_partial(
    model="gpt-5.6",
    response_model=Person,
    messages=[{"role": "user", "content": text}],
):
    print(partial)  # Updates as model streams
```

## Retry Behavior

Instructor auto-retries on validation failure:
```python
client = instructor.from_openai(
    OpenAI(),
    max_retries=3,  # default
)
```
It sends the validation error back to the model for correction.

## Modes

```python
# Default: JSON mode with markdown extraction
instructor.Mode.TOOLS  # Use function calling API (most reliable)
instructor.Mode.JSON   # Use JSON mode
instructor.Mode.MD_JSON # Extract JSON from markdown
```
