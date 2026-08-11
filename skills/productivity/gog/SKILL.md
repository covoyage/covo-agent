---
name: gog
description: "Google Workspace: Gmail, Calendar, Drive, Docs, Sheets via gws CLI or Python API."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [google, gmail, calendar, drive, docs, sheets, workspace]
---

# Google Workspace

```bash
pip install google-api-python-client google-auth-oauthlib

# Gmail: list recent emails
python3 -c "
from google.auth.transport.requests import Request
# ... OAuth setup ...
service.users().messages().list(userId='me', maxResults=10).execute()
"

# Calendar: list events
python3 -c "
service.events().list(calendarId='primary', maxResults=10).execute()
"

# Drive: list files
python3 -c "
service.files().list(pageSize=10, fields='files(name,id)').execute()
"

# Sheets: read data
python3 -c "
sheet.values().get(spreadsheetId='ID', range='A1:D10').execute()
"

# Quick search via Gmail API
python3 -c "
service.users().messages().list(userId='me', q='from:boss').execute()
"
```
