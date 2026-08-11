---
name: dspy
description: "DSPy: declarative LM programs that auto-optimize prompts, chains, and RAG pipelines."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [dspy, prompt-engineering, optimization, rag, llm, nlp]
  related_skills: [instructor, outlines, guidance]
---

# DSPy — Declarative LM Programming

Build LM applications by declaring what you want rather than writing
prompts. DSPy automatically optimizes prompts and fine-tunes for you.

## When to Use

- "optimize the prompts for this pipeline"
- "build a RAG system with auto-tuned prompts"
- "improve the accuracy of my LM-based classifier"
- "compare different LM strategies automatically"

## Installation

```bash
pip install dspy-ai
```

## Key Concepts

| Concept | What It Is |
|---------|------------|
| Signature | I/O specification: "question -> answer" |
| Module | Composable LM program (like PyTorch nn.Module) |
| Optimizer | Automatically improves prompts/few-shot examples |
| Metric | Evaluation function that guides optimization |

## Basic Example

```python
import dspy

lm = dspy.LM('openai/gpt-5.6')
dspy.configure(lm=lm)

# Define the task declaratively
class QA(dspy.Signature):
    """Answer questions based on given context."""
    context = dspy.InputField(desc="relevant information")
    question = dspy.InputField()
    answer = dspy.OutputField(desc="concise answer with citation")

# Build a module
class RAG(dspy.Module):
    def __init__(self):
        self.retrieve = dspy.Retrieve(k=3)
        self.answer = dspy.ChainOfThought(QA)
    
    def forward(self, question):
        context = self.retrieve(question).passages
        return self.answer(context=context, question=question)

# Optimize automatically
from dspy.teleprompt import BootstrapFewShot
optimizer = BootstrapFewShot(metric=dspy.evaluate.answer_exact_match)
optimized_rag = optimizer.compile(RAG(), trainset=trainset)
```

## Common Modules

```python
# Chain of Thought
cot = dspy.ChainOfThought("question -> reasoning, answer")

# Multi-hop reasoning
mh = dspy.MultiChainComparison("question -> answer")

# Program of Thought (code-first)
pot = dspy.ProgramOfThought("question -> code, answer")

# ReAct agent
react = dspy.ReAct("question -> tool_calls, answer")
```

## Optimization Strategies

```python
# Few-shot bootstrapping
BootstrapFewShot(metric=metric)

# Prompt optimization (like automatic prompt engineering)
MIPROv2(metric=metric, auto="light")

# Fine-tune with optimized demonstrations
BootstrapFinetune(metric=metric)
```

## Quick Start Template

```python
import dspy
from dspy.datasets import HotPotQA
from dspy.evaluate import Evaluate
from dspy.teleprompt import BootstrapFewShot

# Setup
lm = dspy.LM('openai/gpt-5.6')
dspy.configure(lm=lm)

# Load data
dataset = HotPotQA(train_seed=42, train_size=20, eval_seed=42, dev_size=50, test_size=0)
trainset = [dspy.Example(**x).with_inputs('question') for x in dataset.train]

# Define
class QA(dspy.Signature):
    question = dspy.InputField()
    answer = dspy.OutputField()

program = dspy.ChainOfThought(QA)

# Optimize
optimizer = BootstrapFewShot(metric=lambda ex, pred, trace=None: 
    dspy.evaluate.answer_exact_match(ex, pred))
optimized = optimizer.compile(program, trainset=trainset)

# Use
result = optimized(question="What is the capital of France?")
print(result.answer)
```
