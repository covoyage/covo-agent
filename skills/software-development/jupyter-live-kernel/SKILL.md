---
name: jupyter-live-kernel
description: "Iterative Python via live Jupyter kernel for data science, exploration, and rapid prototyping."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
  env_vars: []
metadata:
  tags: [python, jupyter, kernel, data-science, exploration, prototype, repl]
  related_skills: [spike, python-debugpy, plan]
---

# Jupyter Live Kernel — Interactive Python Sessions

Launch and interact with a live Jupyter kernel for iterative Python
development. Use this for data exploration, prototyping, testing ideas
quickly, or when the user needs an interactive Python environment.

## When to Use

- "let's try this in Python" / "open a Python session"
- "explore this dataset" / "plot this data"
- "prototype this algorithm" / "test this hypothesis"
- "show me what happens when..." (data exploration)
- "run this and see the output interactively"

Do NOT use for: production scripts (use bash), code editing (use edit_block),
or long-running computation (use process tool).

## Process

### Step 1 — Check/Install Dependencies

```bash
# Check if jupyter is available
python3 -c "import jupyter_client" 2>/dev/null && echo "ready" || \
  pip install jupyter_client ipykernel

# Verify kernel specs
jupyter kernelspec list
```

### Step 2 — Start a Kernel

```python
from jupyter_client import KernelManager
km = KernelManager(kernel_name='python3')
km.start_kernel()
client = km.client()
client.start_channels()
client.wait_for_ready()
```

### Step 3 — Execute Code Interactively

```python
# Execute a cell
client.execute("""
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt

# Your code here
df = pd.DataFrame({'x': range(10), 'y': [i**2 for i in range(10)]})
df.describe()
""")

# Get the result
reply = client.get_shell_msg(timeout=30)
# Handle streaming output from iopub channel
```

### Step 4 — Iterative Workflow

The key advantage of a live kernel is state preservation:

1. **Define once, use repeatedly:** Variables persist across cells
2. **Explore incrementally:** Load data once, query many times
3. **Visualize interactively:** matplotlib/seaborn charts update in-place

```python
# Cell 1: Load data
client.execute("df = pd.read_csv('data.csv')")

# Cell 2: Explore
client.execute("df.head()")
client.execute("df.describe()")
client.execute("df['column'].value_counts()")

# Cell 3: Visualize
client.execute("df['column'].hist()")
client.execute("plt.savefig('/tmp/plot.png')")
```

### Step 5 — When Done

```python
# Clean shutdown
client.stop_channels()
km.shutdown_kernel()
```

## Key Patterns

### Pattern: Data Exploration
Load a dataset once, explore it with multiple queries:
```python
# Load
df = pd.read_csv('...')
# Explore
df.info()
df.describe()
df.corr()
# Visualize
sns.heatmap(df.corr())
```

### Pattern: Algorithm Prototyping
Test algorithms with live feedback:
```python
def new_algorithm(data):
    # Implementation
    pass

# Test immediately
result = new_algorithm(test_data)
assert result == expected
```

### Pattern: Ad-hoc Analysis
Quick one-off analyses without setting up a full script:
```python
# What's the distribution?
df['column'].value_counts().plot(kind='bar')
# Find outliers
df[df['column'] > df['column'].quantile(0.99)]
```

## Tools

- `bash` with `python3` — main execution
- `pip install` — dependency management
- `glob` / `ls` — locate data files
- `canvas` — display generated charts
